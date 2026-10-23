package handlers

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdempotencyKeyGuard(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	tests := []struct {
		name    string
		values  []string
		present bool
		want    int
	}{
		{"absent", nil, false, 204}, {"valid", []string{key}, true, 204},
		{"empty", []string{""}, true, 400}, {"duplicate", []string{key, key}, true, 400},
		{"padding", []string{key + "="}, true, 400}, {"noncanonical", []string{key[:42] + "B"}, true, 400},
		{"short", []string{"private-secret"}, true, 400}, {"whitespace", []string{" " + key}, true, 400},
	}
	h := &Handler{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/orders", nil)
			if tt.present {
				req.Header["Idempotency-Key"] = tt.values
			}
			rr := httptest.NewRecorder()
			h.IdempotencyKeyGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("status=%d want=%d", rr.Code, tt.want)
			}
			if rr.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("response can be cached")
			}
			for _, value := range tt.values {
				if value != "" && strings.Contains(rr.Body.String(), value) {
					t.Fatal("key leaked in validation error")
				}
			}
		})
	}
}
