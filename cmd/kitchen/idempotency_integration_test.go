//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/jackc/pgx/v5/pgxpool"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/handlers"
	"kitchen-api/internal/kitchen/repository"
	"kitchen-api/internal/kitchen/service"
)

type orderFixture struct {
	pool            *pgxpool.Pool
	server          *httptest.Server
	spec            *openapi3.T
	body            api.CreateOrderRequest
	venueID, dishID int64
	logs            *lockedBuffer
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func integrationPool(t *testing.T, env string) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(env)
	if url == "" {
		t.Fatalf("%s required; run make test-postgres", env)
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	f := &orderFixture{pool: integrationPool(t, "TEST_DATABASE_URL"), logs: &lockedBuffer{}}
	token := newTestKey()
	hash := sha256.Sum256([]byte(token))
	err := f.pool.QueryRow(t.Context(), `INSERT INTO establishments(name,accepts_orders,api_key_hash) VALUES('Integration',true,$1) RETURNING id`, hash[:]).Scan(&f.venueID)
	if err != nil {
		t.Fatal(err)
	}
	var categoryID int64
	err = f.pool.QueryRow(t.Context(), `INSERT INTO menu_categories(establishment_id,name) VALUES($1,'Test') RETURNING id`, f.venueID).Scan(&categoryID)
	if err != nil {
		t.Fatal(err)
	}
	err = f.pool.QueryRow(t.Context(), `INSERT INTO dishes(category_id,name,price_minor,is_available) VALUES($1,'Soup',100,true) RETURNING id`, categoryID).Scan(&f.dishID)
	if err != nil {
		t.Fatal(err)
	}
	f.body = api.CreateOrderRequest{EstablishmentID: f.venueID, CustomerName: "Test", CustomerPhone: "12345", DeliveryAddress: "Address", OrderItems: []api.CreateOrderItemRequest{{DishID: f.dishID, Quantity: 2, ExpectedUnitPriceMinor: 100}}}
	f.spec, err = api.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	f.spec.Servers = nil
	repo := repository.New(f.pool)
	h := handlers.New(service.NewEstablishmentService(repo), service.NewMenuService(repo), service.NewOrderService(repo, time.Minute), slog.New(slog.NewJSONHandler(f.logs, nil)))
	f.server = httptest.NewServer(newHTTPHandler(h, f.spec, h.Readiness(f.pool, time.Second), 2*time.Second))
	t.Cleanup(f.server.Close)
	return f
}

func newTestKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (f *orderFixture) post(t *testing.T, key string, body api.CreateOrderRequest) (int, []byte) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+"/api/v1/orders", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Error("missing no-store")
	}
	router, err := legacy.NewRouter(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatal(err)
	}
	validation := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}
	response := &openapi3filter.ResponseValidationInput{RequestValidationInput: validation, Status: res.StatusCode, Header: res.Header, Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	if err := openapi3filter.ValidateResponse(t.Context(), response.SetBodyBytes(data)); err != nil {
		t.Errorf("response violates OpenAPI: %v", err)
	}
	return res.StatusCode, data
}

func (f *orderFixture) count(t *testing.T, key string) (int, int) {
	t.Helper()
	hash := sha256.Sum256([]byte(key))
	var orders, items int
	err := f.pool.QueryRow(t.Context(), `SELECT count(DISTINCT o.id),count(oi.id) FROM orders o LEFT JOIN order_items oi ON oi.order_id=o.id WHERE o.tracking_token_hash=$1`, hash[:]).Scan(&orders, &items)
	if err != nil {
		t.Fatal(err)
	}
	return orders, items
}

func TestIdempotencyConcurrentHTTP(t *testing.T) {
	f := newOrderFixture(t)
	key := newTestKey()
	const n = 12
	statuses := make([]int, n)
	bodies := make([][]byte, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; statuses[i], bodies[i] = f.post(t, key, f.body) }()
	}
	close(start)
	wg.Wait()
	created := 0
	for i, status := range statuses {
		if status == 201 {
			created++
		} else if status != 200 {
			t.Fatalf("unexpected status %d", status)
		}
		if !bytes.Equal(bodies[0], bodies[i]) {
			t.Fatal("replay receipt changed")
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	if orders, items := f.count(t, key); orders != 1 || items != 1 {
		t.Fatalf("orders=%d items=%d", orders, items)
	}
	if strings.Contains(f.logs.String(), key) {
		t.Fatal("key leaked in logs")
	}
	// Live-state changes must never invalidate the persisted receipt.
	hash := sha256.Sum256([]byte(key))
	for _, query := range []string{
		`UPDATE establishments SET accepts_orders=false WHERE id=$1`,
	} {
		if _, err := f.pool.Exec(t.Context(), query, f.venueID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE dishes SET price_minor=999,is_available=false WHERE id=$1`, f.dishID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE orders SET status='confirmed' WHERE tracking_token_hash=$1`, hash[:]); err != nil {
		t.Fatal(err)
	}
	status, replayed := f.post(t, key, f.body)
	if status != 200 || !bytes.Equal(replayed, bodies[0]) {
		t.Fatalf("live-state change broke replay: %d", status)
	}
	changed := f.body
	changed.DeliveryAddress = "Another address"
	status, data := f.post(t, key, changed)
	if status != 409 || !bytes.Contains(data, []byte("idempotency_key_reused")) {
		t.Fatalf("changed body status=%d", status)
	}
}

func TestIdempotencyCompetingBodiesAndLegacy(t *testing.T) {
	f := newOrderFixture(t)
	key := newTestKey()
	changed := f.body
	changed.CustomerPhone = "67890"
	statuses := make(chan int, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, body := range []api.CreateOrderRequest{f.body, changed} {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; status, _ := f.post(t, key, body); statuses <- status }()
	}
	close(start)
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatalf("statuses=%v", counts)
	}
	if orders, items := f.count(t, key); orders != 1 || items != 1 {
		t.Fatalf("orders=%d items=%d", orders, items)
	}
	status, _ := f.post(t, newTestKey(), f.body)
	if status != 201 {
		t.Fatal(status)
	}
	var legacyTokens []string
	for range 2 {
		status, data := f.post(t, "", f.body)
		if status != 201 {
			t.Fatal(status)
		}
		var receipt api.CreatedOrder
		if err := json.Unmarshal(data, &receipt); err != nil {
			t.Fatal(err)
		}
		legacyTokens = append(legacyTokens, receipt.TrackingToken)
	}
	if legacyTokens[0] == legacyTokens[1] {
		t.Fatal("legacy tokens reused")
	}
	status, _ = f.post(t, legacyTokens[0], f.body)
	if status != 409 {
		t.Fatalf("legacy key collision status=%d", status)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+"/api/v1/orders/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Tracking-Token", legacyTokens[0])
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		t.Fatal("legacy tracking broken")
	}
}

func TestIdempotencyInvalidKeyDoesNotLeak(t *testing.T) {
	f := newOrderFixture(t)
	for _, key := range []string{"private-invalid-secret", newTestKey() + "=", strings.Repeat("A", 42) + "B"} {
		status, data := f.post(t, key, f.body)
		if status != 400 {
			t.Fatalf("invalid key status=%d", status)
		}
		if bytes.Contains(data, []byte(key)) || strings.Contains(f.logs.String(), key) {
			t.Fatal("secret leaked")
		}
	}
}

func TestIdempotencyRollbackAndLockCancellation(t *testing.T) {
	f := newOrderFixture(t)
	key := newTestKey()
	hash := sha256.Sum256([]byte(key))
	// A real failure after the order INSERT must roll back both rows and fingerprint.
	_, err := f.pool.Exec(t.Context(), `CREATE FUNCTION reject_test_item() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected item failure'; END $$; CREATE TRIGGER reject_test_item BEFORE INSERT ON order_items FOR EACH ROW EXECUTE FUNCTION reject_test_item()`)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := f.post(t, key, f.body)
	if status != 500 {
		t.Fatalf("injected failure status=%d", status)
	}
	if orders, items := f.count(t, key); orders != 0 || items != 0 {
		t.Fatal("partial order survived rollback")
	}
	_, err = f.pool.Exec(t.Context(), `DROP TRIGGER reject_test_item ON order_items; DROP FUNCTION reject_test_item()`)
	if err != nil {
		t.Fatal(err)
	}
	status, _ = f.post(t, key, f.body)
	if status != 201 {
		t.Fatalf("retry after rollback status=%d", status)
	}
	// Use a second key and hold its lock from a separate transaction.
	key = newTestKey()
	hash = sha256.Sum256([]byte(key))
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(t.Context(), "SELECT pg_advisory_xact_lock($1)", int64(binary.BigEndian.Uint64(hash[:8])))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	svc := service.NewOrderService(repository.New(f.pool), time.Minute)
	_, _, err = svc.Create(ctx, service.CreateOrderInput{Idempotent: true, EstablishmentID: f.venueID, TrackingTokenHash: hash[:], CustomerName: "Test", CustomerPhone: "12345", DeliveryAddress: "Address", Items: []service.CreateOrderItem{{DishID: f.dishID, Quantity: 1, ExpectedUnitPriceMinor: 100}}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if orders, _ := f.count(t, key); orders != 0 {
		t.Fatal("cancelled waiter created order")
	}
}

func TestIdempotencyMigrationWithLegacyOrder(t *testing.T) {
	pool := integrationPool(t, "UPGRADE_DATABASE_URL")
	key := newTestKey()
	hash := sha256.Sum256([]byte(key))
	var venue int64
	err := pool.QueryRow(t.Context(), `INSERT INTO establishments(name,accepts_orders,api_key_hash) VALUES('Legacy',true,$1) RETURNING id`, hash[:]).Scan(&venue)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = pool.QueryRow(t.Context(), `INSERT INTO orders(establishment_id,tracking_token_hash,status,customer_name,customer_phone,delivery_address,total_minor) VALUES($1,$2,'pending_confirmation','Test','12345','Address',0) RETURNING id`, venue, hash[:]).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	goose := os.Getenv("TEST_GOOSE")
	if goose == "" {
		t.Fatal("TEST_GOOSE required")
	}
	migrate := func(direction string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), goose, "-dir", filepath.Join("..", "..", "migrations"), "postgres", os.Getenv("UPGRADE_DATABASE_URL"), direction)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("migration %s: %v: %s", direction, err, output)
		}
	}
	migrate("up")
	order, err := repository.New(pool).GetOrderByTrackingTokenHash(t.Context(), hash[:])
	if err != nil || order.ID != id || order.RequestFingerprint != nil {
		t.Fatalf("legacy order after upgrade: %v", err)
	}
	_, err = pool.Exec(t.Context(), `UPDATE orders SET request_fingerprint=$1 WHERE id=$2`, []byte{1}, id)
	if err == nil {
		t.Fatal("invalid fingerprint accepted")
	}
	migrate("down")
	var count int
	err = pool.QueryRow(t.Context(), `SELECT count(*) FROM orders WHERE id=$1 AND tracking_token_hash=$2`, id, hash[:]).Scan(&count)
	if err != nil || count != 1 {
		t.Fatal("rollback removed legacy order")
	}
	migrate("up")
	// Verify the new repository still accepts the old token after upgrade/down/up.
	_, err = repository.New(pool).GetOrderByTrackingTokenHash(t.Context(), hash[:])
	if err != nil {
		t.Fatal(err)
	}
}
