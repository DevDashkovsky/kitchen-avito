package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"

	"kitchen-api/internal/kitchen/api"
)

func TestRequestGuard(t *testing.T) {
	t.Parallel()

	handler := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	t.Run("passes completed response", func(t *testing.T) {
		t.Parallel()

		guarded := handler.RequestGuard(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Test", "yes")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("created"))
		}))

		response := httptest.NewRecorder()
		guarded.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

		if response.Code != http.StatusCreated || response.Header().Get("X-Test") != "yes" || response.Body.String() != "created" {
			t.Fatalf("response = status %d, headers %v, body %q", response.Code, response.Header(), response.Body.String())
		}
	})

	t.Run("recovers panic as json", func(t *testing.T) {
		t.Parallel()

		guarded := handler.RequestGuard(time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		}))

		response := httptest.NewRecorder()
		guarded.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

		assertErrorResponse(t, response, http.StatusInternalServerError, api.ErrorResponseCodeInternalError)
	})

	t.Run("returns gateway timeout as json", func(t *testing.T) {
		t.Parallel()

		guarded := handler.RequestGuard(10 * time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		}))

		response := httptest.NewRecorder()
		guarded.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

		assertErrorResponse(t, response, http.StatusGatewayTimeout, api.ErrorResponseCodeInternalError)
	})
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	handler := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	t.Run("liveness", func(t *testing.T) {
		t.Parallel()

		response := httptest.NewRecorder()
		handler.Health(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("response = status %d, content-type %q", response.Code, response.Header().Get("Content-Type"))
		}
	})

	t.Run("ready", func(t *testing.T) {
		t.Parallel()

		response := httptest.NewRecorder()
		handler.Readiness(fakePinger{}, time.Second)(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
	})

	t.Run("not ready", func(t *testing.T) {
		t.Parallel()

		response := httptest.NewRecorder()
		handler.Readiness(fakePinger{err: errors.New("database unavailable")}, time.Second)(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", response.Code)
		}
	})
}

func TestJSONRouteErrors(t *testing.T) {
	t.Parallel()

	handler := New(nil, nil, nil, nil)
	for name, testCase := range map[string]struct {
		serve  func(http.ResponseWriter, *http.Request)
		status int
	}{
		"not found":          {serve: handler.NotFound, status: http.StatusNotFound},
		"method not allowed": {serve: handler.MethodNotAllowed, status: http.StatusMethodNotAllowed},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			testCase.serve(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			if response.Code != testCase.status || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("response = status %d, content-type %q", response.Code, response.Header().Get("Content-Type"))
			}
		})
	}
}

func TestValidationFieldsUsesStructuredParameter(t *testing.T) {
	t.Parallel()

	fields := validationFields(&openapi3filter.RequestError{
		Parameter: &openapi3.Parameter{Name: "limit"},
	})
	if len(fields) != 1 || fields[0].Field != "limit" {
		t.Fatalf("fields = %+v, want limit field", fields)
	}
}

type fakePinger struct {
	err error
}

func (p fakePinger) Ping(context.Context) error {
	return p.err
}

func assertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code api.ErrorResponseCode) {
	t.Helper()

	if response.Code != status || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = status %d, content-type %q", response.Code, response.Header().Get("Content-Type"))
	}

	var body api.ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != code {
		t.Fatalf("code = %q, want %q", body.Code, code)
	}
}
