package e2e_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"kitchen-api/internal/kitchen/api"
)

// The runner invokes this test on either side of a real API process restart.
func TestIdempotencyRestart(t *testing.T) {
	phase, path := os.Getenv("RESTART_PHASE"), os.Getenv("RESTART_FIXTURE")
	if phase == "" || path == "" {
		t.Skip("run make test-postgres for the process restart scenario")
	}
	client := newTestClient()
	client.waitForKitchen(t)
	type fixture struct {
		Key     string
		Request api.CreateOrderRequest
		Receipt json.RawMessage
	}
	var saved fixture
	switch phase {
	case "create":
		establishment, dish := client.demoMenu(t)
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			t.Fatal(err)
		}
		saved.Key = base64.RawURLEncoding.EncodeToString(secret)
		// newOrderRequest returns the exact HTTP body used by the original E2E suite.
		payload, err := json.Marshal(newOrderRequest(establishment.ID, dish, 1))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(payload, &saved.Request); err != nil {
			t.Fatal(err)
		}
		status, data, err := client.doRequest(t.Context(), http.MethodPost, "/api/v1/orders", saved.Request, map[string]string{"Idempotency-Key": saved.Key})
		if err != nil || status != 201 {
			t.Fatalf("create status=%d error=%v", status, err)
		}
		saved.Receipt = bytes.TrimSpace(data)
		data, err = json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	case "replay":
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		status, replayed, err := client.doRequest(t.Context(), http.MethodPost, "/api/v1/orders", saved.Request, map[string]string{"Idempotency-Key": saved.Key})
		if err != nil || status != 200 || !bytes.Equal(bytes.TrimSpace(replayed), saved.Receipt) {
			t.Fatalf("restart replay status=%d error=%v receipt_equal=%v", status, err, bytes.Equal(bytes.TrimSpace(replayed), saved.Receipt))
		}
		var receipt api.CreatedOrder
		if err := json.Unmarshal(replayed, &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.TrackingToken != saved.Key {
			t.Fatal("tracking token changed")
		}
		client.getTrackedOrder(t, saved.Key)
	default:
		t.Fatalf("unknown restart phase %q", phase)
	}
}
