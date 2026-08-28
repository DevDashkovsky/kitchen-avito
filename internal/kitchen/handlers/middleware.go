package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	openapimiddleware "github.com/oapi-codegen/nethttp-middleware"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
)

var (
	errAuthentication         = errors.New("authentication failed")
	errAuthenticationInternal = errors.New("authentication internal error")
)

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
			writeJSON(w, http.StatusBadRequest, badRequest("Некорректные данные запроса"))
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.logError("encode response", err)
			writeJSON(w, http.StatusInternalServerError, internalError())
		},
	}
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

	var securityError *openapi3filter.SecurityRequirementsError
	if errors.As(err, &securityError) {
		writeJSON(w, http.StatusUnauthorized, unauthorized())
		return
	}

	status := opts.StatusCode
	if status < 400 || status > 499 {
		status = http.StatusBadRequest
	}

	writeJSON(w, status, badRequest("Некорректные данные запроса"))
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
