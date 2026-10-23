package handlers

import (
	"encoding/base64"
	"net/http"
)

func validIdempotencyKey(key string) bool {
	if len(key) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(key)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == key
}

// IdempotencyKeyGuard runs before generated parameter decoding so duplicate
// header values cannot be silently reduced to the first one.
func (h *Handler) IdempotencyKeyGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/orders" {
			w.Header().Set("Cache-Control", "no-store")
			values, present := r.Header[http.CanonicalHeaderKey("Idempotency-Key")]
			if present && (len(values) != 1 || !validIdempotencyKey(values[0])) {
				writeJSON(w, http.StatusBadRequest, badRequest("Некорректный ключ повтора"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
