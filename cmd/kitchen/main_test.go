package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/handlers"
)

func TestHTTPHandlerRouteErrorsAreJSON(t *testing.T) {
	t.Parallel()

	spec, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("GetSwagger() error = %v", err)
	}
	handler := handlers.New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	readiness := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	router := newHTTPHandler(handler, spec, readiness, time.Second)

	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   api.ErrorResponseCode
	}{
		{name: "not found", method: http.MethodGet, path: "/unknown", status: http.StatusNotFound, code: api.ErrorResponseCodeNotFound},
		{name: "method not allowed", method: http.MethodPut, path: "/api/v1/establishments", status: http.StatusMethodNotAllowed, code: api.ErrorResponseCodeValidationError},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), testCase.method, testCase.path, nil)
			router.ServeHTTP(response, request)
			if response.Code != testCase.status || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("response = status %d, content-type %q", response.Code, response.Header().Get("Content-Type"))
			}

			var body api.ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != testCase.code {
				t.Fatalf("code = %q, want %q", body.Code, testCase.code)
			}
		})
	}
}
