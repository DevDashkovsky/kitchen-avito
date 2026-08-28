package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestGenerateTrackingToken(t *testing.T) {
	t.Parallel()

	token, hash, err := generateTrackingToken()
	if err != nil {
		t.Fatalf("generateTrackingToken() error = %v", err)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded token length = %d, want 32", len(decoded))
	}

	wantHash := sha256.Sum256([]byte(token))
	if !bytes.Equal(hash, wantHash[:]) {
		t.Error("returned hash does not match tracking token")
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{header: "Bearer secret", want: "secret", ok: true},
		{header: "bearer secret", want: "secret", ok: true},
		{header: " Bearer   secret ", want: "secret", ok: true},
		{header: "Basic secret"},
		{header: "Bearer"},
		{header: "Bearer   "},
	}

	for _, test := range tests {
		got, ok := bearerToken(test.header)
		if got != test.want || ok != test.ok {
			t.Errorf("bearerToken(%q) = %q, %t; want %q, %t", test.header, got, ok, test.want, test.ok)
		}
	}
}
