package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"kitchen-api/internal/kitchen/api"
)

const (
	defaultKitchenURL = "http://localhost:8080"
	defaultPartnerKey = "demo-partner-key"
)

type testClient struct {
	baseURL    string
	partnerKey string
	http       *http.Client
}

func TestOrderScenarios(t *testing.T) {
	if os.Getenv("E2E") != "1" {
		t.Skip("set E2E=1 and start postgres, migrations and kitchen")
	}

	client := newTestClient()
	client.waitForKitchen(t)
	establishment, dish := client.demoMenu(t)

	t.Run("price conflict", func(t *testing.T) {
		request := newOrderRequest(establishment.ID, dish, 1)
		request.OrderItems[0].ExpectedUnitPriceMinor++

		var conflict api.OrderConflictResponse
		client.request(t, http.MethodPost, "/api/v1/orders", request, nil, http.StatusConflict, &conflict)

		if string(conflict.Code) != "order_conflict" || len(conflict.Conflicts) != 1 {
			t.Fatalf("conflict response = %+v", conflict)
		}
		item := conflict.Conflicts[0]
		if item.DishID != dish.ID || string(item.Reason) != "price_changed" ||
			item.CurrentUnitPriceMinor == nil || *item.CurrentUnitPriceMinor != dish.PriceMinor {
			t.Fatalf("dish conflict = %+v", item)
		}
	})

	t.Run("buyer cancellation", func(t *testing.T) {
		created := client.createOrder(t, newOrderRequest(establishment.ID, dish, 2))
		if string(created.Status) != "pending_confirmation" || created.TotalMinor != dish.PriceMinor*2 {
			t.Fatalf("created order = %+v", created)
		}

		tracked := client.getTrackedOrder(t, created.TrackingToken)
		if string(tracked.Status) != "pending_confirmation" {
			t.Fatalf("tracked status = %q, want pending_confirmation", tracked.Status)
		}

		for range 2 {
			var cancelled api.CancelledOrder
			client.request(
				t,
				http.MethodPost,
				"/api/v1/orders/cancel",
				nil,
				map[string]string{"X-Tracking-Token": created.TrackingToken},
				http.StatusOK,
				&cancelled,
			)
			if string(cancelled.Status) != "cancelled" {
				t.Fatalf("cancelled status = %q", cancelled.Status)
			}
		}

		tracked = client.getTrackedOrder(t, created.TrackingToken)
		if string(tracked.Status) != "cancelled" {
			t.Fatalf("tracked status = %q, want cancelled", tracked.Status)
		}
	})

	t.Run("partner lifecycle", func(t *testing.T) {
		request := newOrderRequest(establishment.ID, dish, 1)
		request.CustomerName = fmt.Sprintf("E2E lifecycle %d", time.Now().UnixNano())
		created := client.createOrder(t, request)
		orderID := client.findPendingOrder(t, request.CustomerName)

		client.updateStatus(t, orderID, "completed", http.StatusConflict)

		statuses := []string{
			"confirmed",
			"preparing",
			"ready_for_delivery",
			"delivering",
			"completed",
		}
		for _, status := range statuses {
			client.updateStatus(t, orderID, status, http.StatusOK)
		}

		client.updateStatus(t, orderID, "completed", http.StatusOK)

		tracked := client.getTrackedOrder(t, created.TrackingToken)
		if string(tracked.Status) != "completed" || tracked.TotalMinor != dish.PriceMinor {
			t.Fatalf("completed order = %+v", tracked)
		}
	})
}

func newTestClient() *testClient {
	baseURL := os.Getenv("KITCHEN_E2E_URL")
	if baseURL == "" {
		baseURL = defaultKitchenURL
	}
	partnerKey := os.Getenv("PARTNER_API_KEY")
	if partnerKey == "" {
		partnerKey = defaultPartnerKey
	}

	return &testClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		partnerKey: partnerKey,
		http:       &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *testClient) waitForKitchen(t *testing.T) {
	t.Helper()

	endpoint := c.baseURL + "/api/v1/establishments?limit=1"
	deadline := time.Now().Add(15 * time.Second)
	for {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, http.NoBody)
		if err != nil {
			t.Fatalf("create readiness request: %v", err)
		}

		response, err := c.http.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("kitchen is not ready at %s", c.baseURL)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (c *testClient) demoMenu(t *testing.T) (api.Establishment, api.MenuDish) {
	t.Helper()

	var establishments api.EstablishmentListResponse
	path := "/api/v1/establishments?query=" + url.QueryEscape("Демо Кафе")
	c.request(t, http.MethodGet, path, nil, nil, http.StatusOK, &establishments)
	if len(establishments.Establishments) != 1 {
		t.Fatalf("demo establishments count = %d, want 1", len(establishments.Establishments))
	}
	demo := establishments.Establishments[0]
	if !demo.AcceptsOrders {
		t.Fatal("demo establishment does not accept orders")
	}

	var menu api.MenuResponse
	c.request(
		t,
		http.MethodGet,
		fmt.Sprintf("/api/v1/establishments/%d/menu", demo.ID),
		nil,
		nil,
		http.StatusOK,
		&menu,
	)
	for _, category := range menu.Categories {
		for _, dish := range category.Dishes {
			if dish.IsAvailable {
				return demo, dish
			}
		}
	}

	t.Fatal("demo menu has no available dishes")
	return api.Establishment{}, api.MenuDish{}
}

func newOrderRequest(establishmentID int64, dish api.MenuDish, quantity int32) api.CreateOrderRequest {
	return api.CreateOrderRequest{
		EstablishmentID: establishmentID,
		CustomerName:    "E2E Покупатель",
		CustomerPhone:   "+79990000000",
		DeliveryAddress: "Новосибирск, Ленина, 1",
		OrderItems: []api.CreateOrderItemRequest{
			{
				DishID:                 dish.ID,
				Quantity:               quantity,
				ExpectedUnitPriceMinor: dish.PriceMinor,
			},
		},
	}
}

func (c *testClient) createOrder(t *testing.T, request api.CreateOrderRequest) api.CreatedOrder {
	t.Helper()

	var created api.CreatedOrder
	c.request(t, http.MethodPost, "/api/v1/orders", request, nil, http.StatusCreated, &created)
	if created.TrackingToken == "" {
		t.Fatal("created order has empty tracking token")
	}

	return created
}

func (c *testClient) getTrackedOrder(t *testing.T, trackingToken string) api.TrackedOrder {
	t.Helper()

	var tracked api.TrackedOrder
	c.request(
		t,
		http.MethodGet,
		"/api/v1/orders/status",
		nil,
		map[string]string{"X-Tracking-Token": trackingToken},
		http.StatusOK,
		&tracked,
	)
	return tracked
}

func (c *testClient) findPendingOrder(t *testing.T, customerName string) int64 {
	t.Helper()

	var orders api.PartnerOrderListResponse
	c.request(
		t,
		http.MethodGet,
		"/api/v1/partner/orders?status=pending_confirmation&limit=100",
		nil,
		c.partnerHeaders(),
		http.StatusOK,
		&orders,
	)
	for _, order := range orders.Orders {
		if order.CustomerName == customerName {
			return order.ID
		}
	}

	t.Fatalf("pending order for %q not found", customerName)
	return 0
}

func (c *testClient) updateStatus(t *testing.T, orderID int64, status string, wantStatus int) {
	t.Helper()

	var response api.UpdateOrderStatusResponse
	var result any
	if wantStatus == http.StatusOK {
		result = &response
	}
	c.request(
		t,
		http.MethodPatch,
		fmt.Sprintf("/api/v1/partner/orders/%d/status", orderID),
		map[string]string{"status": status},
		c.partnerHeaders(),
		wantStatus,
		result,
	)
	if wantStatus == http.StatusOK && (response.ID != orderID || string(response.Status) != status) {
		t.Fatalf("status response = %+v, want order %d in %s", response, orderID, status)
	}
}

func (c *testClient) partnerHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + c.partnerKey}
}

func (c *testClient) request(
	t *testing.T,
	method string,
	path string,
	body any,
	headers map[string]string,
	wantStatus int,
	result any,
) {
	t.Helper()

	reader := io.Reader(http.NoBody)
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode %s %s request: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(t.Context(), method, c.baseURL+path, reader)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response, err := c.http.Do(request)
	if err != nil {
		t.Fatalf("execute %s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf(
			"%s %s status = %d, want %d, body = %s",
			method,
			path,
			response.StatusCode,
			wantStatus,
			responseBody,
		)
	}
	if result != nil {
		if err := json.Unmarshal(responseBody, result); err != nil {
			t.Fatalf("decode %s %s response: %v, body = %s", method, path, err, responseBody)
		}
	}
}
