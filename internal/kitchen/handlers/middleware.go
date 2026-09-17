package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	openapimiddleware "github.com/oapi-codegen/nethttp-middleware"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
)

var (
	errAuthentication         = errors.New("authentication failed")
	errAuthenticationInternal = errors.New("authentication internal error")
)

const maxRequestBodyBytes int64 = 1 << 20

type databasePinger interface {
	Ping(context.Context) error
}

func (h *Handler) OpenAPIValidator(spec *openapi3.T) func(http.Handler) http.Handler {
	return openapimiddleware.OapiRequestValidatorWithOptions(spec, &openapimiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: h.authenticate,
		},
		ErrorHandlerWithOpts: h.validationErrorHandler,
		DoNotValidateServers: true,
	})
}

func (h *Handler) StrictServerOptions() api.StrictHTTPServerOptions {
	return api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.logError("decode request", err)
			if requestTooLarge(err) {
				writeJSON(w, http.StatusRequestEntityTooLarge, badRequest("Тело запроса слишком большое"))
				return
			}
			writeJSON(w, http.StatusBadRequest, badRequest("Некорректные данные запроса"))
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.logError("encode response", err)
			writeJSON(w, http.StatusInternalServerError, internalError())
		},
	}
}

func (h *Handler) RequestBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRequestBodyBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, badRequest("Тело запроса слишком большое"))
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		responseWriter := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(responseWriter, r)

		status := responseWriter.Status()
		if status == 0 {
			status = http.StatusOK
		}

		h.logger.Info(
			"request completed",
			"method", r.Method,
			"route", chi.RouteContext(r.Context()).RoutePattern(),
			"status", status,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"request_id", chimiddleware.GetReqID(r.Context()),
		)
	})
}

// RequestGuard isolates response writes until the handler has completed. This
// lets the service return a consistent JSON error for both panics and request
// deadlines without racing a handler that is unwinding after context cancel.
func (h *Handler) RequestGuard(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			buffered := newBufferedResponseWriter()
			result := make(chan requestResult, 1)
			go func() {
				outcome := requestResult{}
				defer func() {
					if recovered := recover(); recovered != nil {
						outcome.panicValue = recovered
						outcome.stack = debug.Stack()
					}
					result <- outcome
				}()

				next.ServeHTTP(buffered, r.WithContext(ctx))
			}()

			select {
			case outcome := <-result:
				if outcome.panicValue != nil {
					h.logger.Error(
						"request panicked",
						"panic", outcome.panicValue,
						"stack", string(outcome.stack),
					)
					writeJSON(w, http.StatusInternalServerError, internalError())
					return
				}
				buffered.FlushTo(w)
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					writeJSON(w, http.StatusGatewayTimeout, gatewayTimeout())
				}
			}
		})
	}
}

func (h *Handler) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Readiness(pinger databasePinger, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		if err := pinger.Ping(ctx); err != nil {
			h.logError("readiness check", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (h *Handler) NotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, notFound())
}

func (h *Handler) MethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, errorResponse(
		api.ErrorResponseCodeValidationError,
		"Метод не поддерживается",
	))
}

type requestResult struct {
	panicValue any
	stack      []byte
}

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

func (w *bufferedResponseWriter) FlushTo(destination http.ResponseWriter) {
	for name, values := range w.header {
		destination.Header()[name] = append([]string(nil), values...)
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	destination.WriteHeader(status)
	_, _ = destination.Write(w.body.Bytes())
}

func (h *Handler) authenticate(
	ctx context.Context,
	input *openapi3filter.AuthenticationInput,
) error {
	request := input.RequestValidationInput.Request

	switch input.SecuritySchemeName {
	case "PartnerApiKey":
		apiKey, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			return errAuthentication
		}

		hash := sha256.Sum256([]byte(apiKey))
		establishmentID, err := h.establishments.Authenticate(ctx, hash[:])
		if errors.Is(err, domain.ErrUnauthorized) {
			return errAuthentication
		}
		if err != nil {
			h.logError("authenticate partner", err)
			return errAuthenticationInternal
		}

		*request = *request.WithContext(context.WithValue(
			request.Context(),
			establishmentIDContextKey,
			establishmentID,
		))
		return nil

	case "TrackingToken":
		trackingToken := strings.TrimSpace(request.Header.Get("X-Tracking-Token"))
		if trackingToken == "" {
			return errAuthentication
		}

		hash := sha256.Sum256([]byte(trackingToken))
		*request = *request.WithContext(context.WithValue(
			request.Context(),
			trackingTokenHashContextKey,
			hash[:],
		))
		return nil

	default:
		h.logError("authenticate request", fmt.Errorf("unknown security scheme %q", input.SecuritySchemeName))
		return errAuthenticationInternal
	}
}

func (h *Handler) validationErrorHandler(
	_ context.Context,
	err error,
	w http.ResponseWriter,
	_ *http.Request,
	opts openapimiddleware.ErrorHandlerOpts,
) {
	if errors.Is(err, errAuthenticationInternal) {
		writeJSON(w, http.StatusInternalServerError, internalError())
		return
	}
	if requestTooLarge(err) {
		writeJSON(w, http.StatusRequestEntityTooLarge, badRequest("Тело запроса слишком большое"))
		return
	}

	var securityError *openapi3filter.SecurityRequirementsError
	if errors.As(err, &securityError) {
		writeJSON(w, http.StatusUnauthorized, unauthorized())
		return
	}

	status := opts.StatusCode
	if status < 400 || status > 499 {
		status = http.StatusBadRequest
	}

	writeJSON(w, status, badRequestWithFields(
		"Некорректные данные запроса",
		validationFields(err),
	))
}

func validationFields(err error) []api.FieldError {
	fields := make([]api.FieldError, 0, 1)

	var requestError *openapi3filter.RequestError
	if errors.As(err, &requestError) && requestError.Parameter != nil {
		fields = append(fields, api.FieldError{
			Field:   requestError.Parameter.Name,
			Message: "Некорректное значение параметра",
		})
		return fields
	}

	var schemaError *openapi3.SchemaError
	if errors.As(err, &schemaError) {
		pointer := schemaError.JSONPointer()
		if len(pointer) > 0 {
			fields = append(fields, api.FieldError{
				Field:   strings.Join(pointer, "."),
				Message: "Некорректное значение поля",
			})
		}
	}

	return fields
}

func requestTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func bearerToken(authorization string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(authorization), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	token = strings.TrimSpace(token)
	return token, token != ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
