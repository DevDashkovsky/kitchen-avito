package e2e_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"kitchen-api/internal/kitchen/api"
)

func TestDishExternalIDRestart(t *testing.T) {
	phase, path := os.Getenv("RESTART_PHASE"), os.Getenv("EXTERNAL_ID_RESTART_FIXTURE")
	if phase == "" || path == "" {
		t.Skip("run make test-postgres for the external ID restart scenario")
	}
	client := newTestClient()
	client.waitForKitchen(t)
	var saved struct {
		DishID     int64
		ExternalID string
	}
	switch phase {
	case "create":
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			t.Fatal(err)
		}
		saved.ExternalID = "restart-" + hex.EncodeToString(random)
		var category api.Category
		client.request(t, http.MethodPost, "/api/v1/partner/categories", map[string]string{"name": saved.ExternalID}, client.partnerHeaders(), 201, &category)
		var dish api.Dish
		client.request(t, http.MethodPost, "/api/v1/partner/dishes", map[string]any{"category_id": category.ID, "name": "Restart dish", "price_minor": 100, "external_id": saved.ExternalID}, client.partnerHeaders(), 201, &dish)
		saved.DishID = dish.ID
		if dish.ExternalID == nil || *dish.ExternalID != saved.ExternalID {
			t.Fatal("creation lost external ID")
		}
		data, err := json.Marshal(saved)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	case "replay":
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		for _, lookup := range []string{"/api/v1/partner/dishes/by-external-id?external_id=" + url.QueryEscape(saved.ExternalID), fmt.Sprintf("/api/v1/partner/dishes/%d", saved.DishID)} {
			var dish api.Dish
			client.request(t, http.MethodGet, lookup, nil, client.partnerHeaders(), 200, &dish)
			if dish.ID != saved.DishID || dish.ExternalID == nil || *dish.ExternalID != saved.ExternalID {
				t.Fatal("restart changed mapping")
			}
		}
	default:
		t.Fatalf("unknown restart phase %q", phase)
	}
}
