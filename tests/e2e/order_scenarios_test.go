package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
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
		t.Skip("set E2E=1 and start postgres, migrations and kitchen without establishment worker")
	}

	client := newTestClient()
	client.waitForKitchen(t)
	establishment, dish := client.demoMenu(t)

	t.Run("service boundary", func(t *testing.T) {
		var health map[string]string
		client.request(t, http.MethodGet, "/healthz", nil, nil, http.StatusOK, &health)
		if health["status"] != "ok" {
			t.Fatalf("health response = %+v", health)
		}

		var routeError api.ErrorResponse
		client.request(t, http.MethodGet, "/unknown", nil, nil, http.StatusNotFound, &routeError)
		if routeError.Code != api.ErrorResponseCodeNotFound {
			t.Fatalf("unknown route response = %+v", routeError)
		}

		client.request(
			t,
			http.MethodPut,
			"/api/v1/establishments",
			nil,
			nil,
			http.StatusMethodNotAllowed,
			nil,
		)
	})

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

	t.Run("partner rejection", func(t *testing.T) {
		request := newOrderRequest(establishment.ID, dish, 1)
		request.CustomerName = fmt.Sprintf("E2E rejection %d", time.Now().UnixNano())
		created := client.createOrder(t, request)
		orderID := client.findPendingOrder(t, request.CustomerName)

		client.updateStatusBody(t, orderID, map[string]string{
			"status":           "rejected",
			"rejection_reason": "overloaded",
		}, http.StatusOK)

		tracked := client.getTrackedOrder(t, created.TrackingToken)
		if string(tracked.Status) != "rejected" || tracked.RejectionReason == nil ||
			string(*tracked.RejectionReason) != "overloaded" {
			t.Fatalf("rejected order = %+v", tracked)
		}
	})

	t.Run("cancel confirm race", func(t *testing.T) {
		request := newOrderRequest(establishment.ID, dish, 1)
		request.CustomerName = fmt.Sprintf("E2E race %d", time.Now().UnixNano())
		created := client.createOrder(t, request)
		orderID := client.findPendingOrder(t, request.CustomerName)

		type raceResult struct {
			operation string
			status    int
			err       error
		}

		start := make(chan struct{})
		results := make(chan raceResult, 2)
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		ctx := t.Context()

		go func() {
			defer waitGroup.Done()
			<-start
			status, _, err := client.doRequest(
				ctx,
				http.MethodPost,
				"/api/v1/orders/cancel",
				nil,
				map[string]string{"X-Tracking-Token": created.TrackingToken},
			)
			results <- raceResult{operation: "cancel", status: status, err: err}
		}()
		go func() {
			defer waitGroup.Done()
			<-start
			status, _, err := client.doRequest(
				ctx,
				http.MethodPatch,
				fmt.Sprintf("/api/v1/partner/orders/%d/status", orderID),
				map[string]string{"status": "confirmed"},
				client.partnerHeaders(),
			)
			results <- raceResult{operation: "confirm", status: status, err: err}
		}()

		close(start)
		waitGroup.Wait()
		close(results)

		successes := 0
		conflicts := 0
		for result := range results {
			if result.err != nil {
				t.Fatalf("%s request failed: %v", result.operation, result.err)
			}
			switch result.status {
			case http.StatusOK:
				successes++
			case http.StatusConflict:
				conflicts++
			default:
				t.Fatalf("%s status = %d, want 200 or 409", result.operation, result.status)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("race results: successes = %d, conflicts = %d", successes, conflicts)
		}

		tracked := client.getTrackedOrder(t, created.TrackingToken)
		if status := string(tracked.Status); status != "cancelled" && status != "confirmed" {
			t.Fatalf("status after race = %q, want cancelled or confirmed", status)
		}
	})

	t.Run("confirmation timeout", func(t *testing.T) {
		request := newOrderRequest(establishment.ID, dish, 1)
		request.CustomerName = fmt.Sprintf("E2E timeout %d", time.Now().UnixNano())
		created := client.createOrder(t, request)

		deadline := time.Now().Add(durationFromEnv(t, "E2E_CONFIRMATION_WAIT", 3*time.Minute))
		for time.Now().Before(deadline) {
			tracked := client.getTrackedOrder(t, created.TrackingToken)
			if string(tracked.Status) == "rejected" {
				if tracked.RejectionReason == nil ||
					string(*tracked.RejectionReason) != "confirmation_timeout" {
					t.Fatalf("timed out order = %+v", tracked)
				}
				return
			}
			if string(tracked.Status) != "pending_confirmation" {
				t.Fatalf("status before timeout = %q", tracked.Status)
			}
			time.Sleep(100 * time.Millisecond)
		}

		t.Fatal("order was not rejected by confirmation timeout")
	})

	t.Run("request limits", func(t *testing.T) {
		exactItems := newRepeatedOrderRequest(establishment.ID, dish, 50, 1)
		created := client.createOrder(t, exactItems)
		if created.TotalMinor != dish.PriceMinor*50 {
			t.Fatalf("50 item total = %d, want %d", created.TotalMinor, dish.PriceMinor*50)
		}

		tooManyItems := newRepeatedOrderRequest(establishment.ID, dish, 51, 1)
		client.request(
			t,
			http.MethodPost,
			"/api/v1/orders",
			tooManyItems,
			nil,
			http.StatusBadRequest,
			nil,
		)

		exactQuantity := newRepeatedOrderRequest(establishment.ID, dish, 2, 50)
		created = client.createOrder(t, exactQuantity)
		if created.TotalMinor != dish.PriceMinor*100 {
			t.Fatalf("quantity 100 total = %d, want %d", created.TotalMinor, dish.PriceMinor*100)
		}

		tooLargeQuantity := newRepeatedOrderRequest(establishment.ID, dish, 2, 50)
		tooLargeQuantity.OrderItems[1].Quantity = 51
		client.request(
			t,
			http.MethodPost,
			"/api/v1/orders",
			tooLargeQuantity,
			nil,
			http.StatusBadRequest,
			nil,
		)

		oversized := newOrderRequest(establishment.ID, dish, 1)
		comment := strings.Repeat("x", (1<<20)+1)
		oversized.Comment = &comment
		client.request(
			t,
			http.MethodPost,
			"/api/v1/orders",
			oversized,
			nil,
			http.StatusRequestEntityTooLarge,
			nil,
		)
	})

	t.Run("partner cursor pagination", func(t *testing.T) {
		wanted := make(map[int64]struct{}, 3)
		for index := range 3 {
			request := newOrderRequest(establishment.ID, dish, 1)
			request.CustomerName = fmt.Sprintf("E2E pagination %d-%d", time.Now().UnixNano(), index)
			client.createOrder(t, request)
			orderID := client.findPendingOrder(t, request.CustomerName)
			wanted[orderID] = struct{}{}
			client.updateStatusBody(t, orderID, map[string]string{
				"status":           "rejected",
				"rejection_reason": "overloaded",
			}, http.StatusOK)
		}

		seen := make(map[int64]struct{}, len(wanted))
		var cursor *string
		for page := 0; page < 100; page++ {
			path := "/api/v1/partner/orders?status=rejected&limit=2"
			if cursor != nil {
				path += "&cursor=" + url.QueryEscape(*cursor)
			}

			var response api.PartnerOrderListResponse
			client.request(t, http.MethodGet, path, nil, client.partnerHeaders(), http.StatusOK, &response)
			for _, order := range response.Orders {
				if _, ok := wanted[order.ID]; ok {
					seen[order.ID] = struct{}{}
				}
			}
			cursor = response.NextCursor
			if cursor == nil {
				break
			}
		}
		if len(seen) != len(wanted) {
			t.Fatalf("paginated rejected orders found %d of %d", len(seen), len(wanted))
		}

		client.request(
			t,
			http.MethodGet,
			"/api/v1/partner/orders?status=rejected&cursor=invalid",
			nil,
			client.partnerHeaders(),
			http.StatusBadRequest,
			nil,
		)
	})
}

func durationFromEnv(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()

	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return duration
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

	endpoint := c.baseURL + "/readyz"
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

func newRepeatedOrderRequest(
	establishmentID int64,
	dish api.MenuDish,
	itemCount int,
	quantity int32,
) api.CreateOrderRequest {
	request := newOrderRequest(establishmentID, dish, quantity)
	request.OrderItems = make([]api.CreateOrderItemRequest, itemCount)
	for index := range request.OrderItems {
		request.OrderItems[index] = api.CreateOrderItemRequest{
			DishID:                 dish.ID,
			Quantity:               quantity,
			ExpectedUnitPriceMinor: dish.PriceMinor,
		}
	}
	return request
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
	c.updateStatusBody(t, orderID, map[string]string{"status": status}, wantStatus)
}

func (c *testClient) updateStatusBody(
	t *testing.T,
	orderID int64,
	body map[string]string,
	wantStatus int,
) {
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
		body,
		c.partnerHeaders(),
		wantStatus,
		result,
	)
	if wantStatus == http.StatusOK &&
		(response.ID != orderID || string(response.Status) != body["status"]) {
		t.Fatalf("status response = %+v, want order %d in %s", response, orderID, body["status"])
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

	status, responseBody, err := c.doRequest(t.Context(), method, path, body, headers)
	if err != nil {
		t.Fatalf("execute %s %s: %v", method, path, err)
	}
	if status != wantStatus {
		t.Fatalf(
			"%s %s status = %d, want %d, body = %s",
			method,
			path,
			status,
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

func (c *testClient) doRequest(
	ctx context.Context,
	method string,
	path string,
	body any,
	headers map[string]string,
) (int, []byte, error) {
	reader := io.Reader(http.NoBody)
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}

	return response.StatusCode, responseBody, nil
}
