//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
	"kitchen-api/internal/kitchen/repository"
)

func dishPath(id int64) string { return fmt.Sprintf("/api/v1/partner/dishes/%d", id) }
func externalLookup(id string) string {
	return "/api/v1/partner/dishes/by-external-id?external_id=" + url.QueryEscape(id)
}
func (f *orderFixture) menuRequest(t *testing.T, method, path string, body any, want int) statusHTTPResult {
	t.Helper()
	r := f.statusRequest(t.Context(), method, path, body, "Authorization", "Bearer "+f.partnerKey)
	f.checkStatusResponse(t, r, want)
	return r
}
func decodeDish(t *testing.T, r statusHTTPResult) api.Dish {
	t.Helper()
	var dish api.Dish
	if err := json.Unmarshal(r.body, &dish); err != nil {
		t.Fatal(err)
	}
	return dish
}
func (f *orderFixture) dishPayload(t *testing.T) map[string]any {
	t.Helper()
	dish, err := repository.New(f.pool).GetDish(t.Context(), f.venueID, f.dishID)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"category_id": dish.CategoryID, "name": "New dish", "price_minor": 100, "is_available": true}
}
func (f *orderFixture) bind(t *testing.T, id int64, external string, want int) statusHTTPResult {
	t.Helper()
	return f.menuRequest(t, http.MethodPut, dishPath(id)+"/external-id", map[string]any{"external_id": external}, want)
}
func (f *orderFixture) readDish(t *testing.T, id int64) domain.Dish {
	t.Helper()
	dish, err := repository.New(f.pool).GetDish(t.Context(), f.venueID, id)
	if err != nil {
		t.Fatal(err)
	}
	return dish
}
func (f *orderFixture) extraCategory(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(t.Context(), "INSERT INTO menu_categories(establishment_id,name) VALUES($1,$2) RETURNING id", f.venueID, newTestKey()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestExternalDishIDLifecycle(t *testing.T) {
	f := newOrderFixture(t)
	payload := f.dishPayload(t)
	payload["external_id"] = "000-SKU.A_:1"
	created := decodeDish(t, f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", payload, 201))
	if created.ExternalID == nil || *created.ExternalID != "000-SKU.A_:1" {
		t.Fatal("external ID missing")
	}
	for _, path := range []string{dishPath(created.ID), externalLookup(*created.ExternalID)} {
		got := decodeDish(t, f.menuRequest(t, http.MethodGet, path, nil, 200))
		if !reflect.DeepEqual(got, created) {
			t.Fatalf("lookup changed dish: %+v", got)
		}
	}
	old := f.readDish(t, created.ID)
	payload["price_minor"] = 999
	conflict := f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", payload, 409)
	if !bytes.Contains(conflict.body, []byte("external_id_conflict")) || !reflect.DeepEqual(old, f.readDish(t, created.ID)) {
		t.Fatal("duplicate POST changed dish")
	}
	category := f.extraCategory(t)
	payload["category_id"] = category
	f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", payload, 409)
	f.menuRequest(t, http.MethodPatch, dishPath(created.ID), map[string]any{"category_id": category, "price_minor": 123, "is_available": false}, 200)
	moved := f.readDish(t, created.ID)
	if moved.CategoryID != category || moved.PriceMinor != 123 || moved.ExternalID == nil || *moved.ExternalID != *created.ExternalID || moved.IsAvailable {
		t.Fatal("PATCH lost data")
	}
	f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", payload, 409) // inactive IDs remain reserved
	foreign := newOrderFixture(t)
	otherPayload := foreign.dishPayload(t)
	otherPayload["external_id"] = *created.ExternalID
	other := decodeDish(t, foreign.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", otherPayload, 201))
	if other.ID == created.ID {
		t.Fatal("tenant identities merged")
	}
	if got := decodeDish(t, foreign.menuRequest(t, http.MethodGet, externalLookup(*created.ExternalID), nil, 200)); got.ID != other.ID {
		t.Fatal("lookup crossed tenant")
	}
	// Case-sensitive identity, same owner.
	payload["external_id"] = "000-sku.A_:1"
	f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", payload, 201)
}

func TestExternalDishIDLegacyBindingAndOrderMapping(t *testing.T) {
	f := newOrderFixture(t)
	orderID, key, _ := f.orderAtStatus(t, "pending_confirmation")
	beforeOrder := f.storedOrder(t, orderID)
	legacy := decodeDish(t, f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", f.dishPayload(t), 201))
	if legacy.ExternalID != nil {
		t.Fatal("legacy creation received a binding")
	}
	before := f.readDish(t, f.dishID)
	first := f.bind(t, f.dishID, "SKU-legacy", 200)
	saved := f.readDish(t, f.dishID)
	again := f.bind(t, f.dishID, "SKU-legacy", 200)
	if !bytes.Equal(first.body, again.body) || !reflect.DeepEqual(saved, f.readDish(t, f.dishID)) {
		t.Fatal("repeat wrote the binding")
	}
	if saved.ExternalID == nil || *saved.ExternalID != "SKU-legacy" || saved.Name != before.Name || saved.PriceMinor != before.PriceMinor || !saved.CreatedAt.Equal(before.CreatedAt) || !saved.UpdatedAt.After(before.UpdatedAt) {
		t.Fatal("binding changed content")
	}
	changed := f.bind(t, f.dishID, "OTHER", 409)
	if !bytes.Contains(changed.body, []byte("external_id_already_bound")) {
		t.Fatalf("wrong conflict: %s", changed.body)
	}
	f.bind(t, legacy.ID, "SKU-legacy", 409)
	if f.readDish(t, legacy.ID).ExternalID != nil {
		t.Fatal("conflict left a binding")
	}
	f.bind(t, legacy.ID, "UNUSED", 200) // transaction recovered after unique violation
	if !reflect.DeepEqual(beforeOrder, f.storedOrder(t, orderID)) {
		t.Fatal("binding changed historical order")
	}
	partner := f.menuRequest(t, http.MethodGet, fmt.Sprintf("/api/v1/partner/orders/%d", orderID), nil, 200)
	var order api.PartnerOrder
	if err := json.Unmarshal(partner.body, &order); err != nil {
		t.Fatal(err)
	}
	mapped := decodeDish(t, f.menuRequest(t, http.MethodGet, dishPath(order.OrderItems[0].DishID), nil, 200))
	if mapped.ExternalID == nil || *mapped.ExternalID != "SKU-legacy" {
		t.Fatal("order mapping not recoverable")
	}
	// Every shared/public shape must omit the private mapping, including conflicts.
	for _, r := range []statusHTTPResult{
		f.statusRequest(t.Context(), http.MethodGet, fmt.Sprintf("/api/v1/establishments/%d/menu", f.venueID), nil, "", ""),
		f.menuRequest(t, http.MethodGet, "/api/v1/partner/menu", nil, 200),
		f.statusRequest(t.Context(), http.MethodGet, "/api/v1/orders/status", nil, "X-Tracking-Token", key),
		partner,
	} {
		f.checkStatusResponse(t, r, 200)
		if bytes.Contains(r.body, []byte("external_id")) || bytes.Contains(r.body, []byte("SKU-legacy")) {
			t.Fatal("private ID leaked into shared response")
		}
	}
	code, receipt := f.post(t, newTestKey(), f.body)
	if code != 201 || bytes.Contains(receipt, []byte("external_id")) {
		t.Fatal("external ID in receipt")
	}
	request := f.body
	request.OrderItems = append([]api.CreateOrderItemRequest(nil), f.body.OrderItems...)
	request.OrderItems[0].ExpectedUnitPriceMinor++
	code, conflict := f.post(t, newTestKey(), request)
	if code != 409 || bytes.Contains(conflict, []byte("external_id")) {
		t.Fatal("external ID in order conflict")
	}
}

func TestExternalDishIDValidationAndIsolation(t *testing.T) {
	f := newOrderFixture(t)
	for _, value := range []any{"", nil, " bad", "bad ", "a/b", "SKU\n", "товар", strings.Repeat("a", 129)} {
		p := f.dishPayload(t)
		p["external_id"] = value
		f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", p, 400)
		f.menuRequest(t, http.MethodPut, dishPath(f.dishID)+"/external-id", map[string]any{"external_id": value}, 400)
	}
	for _, path := range []string{"/api/v1/partner/dishes/by-external-id", externalLookup(""), externalLookup("x") + "&external_id=x", externalLookup("x") + "&external_id=%ZZ", externalLookup("x") + "&external_id=bad;value", externalLookup("bad value"), externalLookup("товар"), dishPath(0)} {
		f.menuRequest(t, http.MethodGet, path, nil, 400)
	}
	f.menuRequest(t, http.MethodPut, dishPath(f.dishID)+"/external-id", map[string]any{}, 400)
	f.menuRequest(t, http.MethodPut, dishPath(f.dishID)+"/external-id", nil, 400)
	f.menuRequest(t, http.MethodPatch, dishPath(f.dishID), map[string]any{"external_id": "x"}, 400)
	maxID := strings.Repeat("Z", 128)
	f.bind(t, f.dishID, maxID, 200)
	f.menuRequest(t, http.MethodGet, externalLookup(maxID), nil, 200)
	foreign := newOrderFixture(t)
	for _, secret := range []string{"", "Bearer wrong"} {
		for _, op := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, dishPath(f.dishID), nil}, {http.MethodGet, externalLookup(maxID), nil},
			{http.MethodPut, dishPath(f.dishID) + "/external-id", map[string]string{"external_id": "x"}},
		} {
			f.checkStatusResponse(t, f.statusRequest(t.Context(), op.method, op.path, op.body, "Authorization", secret), 401)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		path, missing := dishPath(f.dishID), dishPath(9223372036854775807)
		var body any
		if method == http.MethodPut {
			path += "/external-id"
			missing += "/external-id"
			body = map[string]string{"external_id": "x"}
		}
		a := foreign.menuRequest(t, method, path, body, 404)
		b := foreign.menuRequest(t, method, missing, body, 404)
		if !bytes.Equal(a.body, b.body) {
			t.Fatal("foreign existence disclosed")
		}
	}
	foreign.menuRequest(t, http.MethodGet, externalLookup(maxID), nil, 404)
	f.menuRequest(t, http.MethodPatch, dishPath(f.dishID), map[string]any{"category_id": foreign.extraCategory(t)}, 404)
	p := f.dishPayload(t)
	p["category_id"] = foreign.extraCategory(t)
	p["external_id"] = "foreign"
	f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", p, 404)
	if *f.readDish(t, f.dishID).ExternalID != maxID {
		t.Fatal("invalid requests changed binding")
	}
}

func TestExternalDishIDRaces(t *testing.T) {
	for _, mode := range []string{"create", "two-dishes", "two-values", "same-binding", "patch"} {
		t.Run(mode, func(t *testing.T) {
			f := newOrderFixture(t)
			type operation struct {
				method, path string
				body         any
			}
			ops := []operation{{http.MethodPut, dishPath(f.dishID) + "/external-id", map[string]string{"external_id": "RACE"}}, {http.MethodPut, dishPath(f.dishID) + "/external-id", map[string]string{"external_id": "RACE"}}}
			category := f.extraCategory(t)
			switch mode {
			case "create":
				p := f.dishPayload(t)
				p["external_id"] = "RACE"
				ops = []operation{{http.MethodPost, "/api/v1/partner/dishes", p}, {http.MethodPost, "/api/v1/partner/dishes", p}}
			case "two-dishes":
				other := decodeDish(t, f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", f.dishPayload(t), 201))
				ops[1].path = dishPath(other.ID) + "/external-id"
			case "two-values":
				ops[1].body = map[string]string{"external_id": "OTHER"}
			case "patch":
				ops[1] = operation{http.MethodPatch, dishPath(f.dishID), map[string]any{"price_minor": 777, "category_id": category}}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			start := make(chan struct{})
			results := make(chan statusHTTPResult, 2)
			for _, op := range ops {
				go func() {
					<-start
					results <- f.statusRequest(ctx, op.method, op.path, op.body, "Authorization", "Bearer "+f.partnerKey)
				}()
			}
			close(start)
			replies := make([]statusHTTPResult, 0, 2)
			counts := map[int]int{}
			for range 2 {
				select {
				case r := <-results:
					if r.err != nil {
						t.Fatal(r.err)
					}
					if r.status != 200 && r.status != 201 && r.status != 409 {
						t.Fatalf("race status %d: %s", r.status, r.body)
					}
					f.checkStatusResponse(t, r, r.status)
					replies = append(replies, r)
					counts[r.status]++
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if mode == "create" {
				if counts[201] != 1 || counts[409] != 1 {
					t.Fatalf("create results: %v", counts)
				}
			} else if mode == "same-binding" || mode == "patch" {
				if counts[200] != 2 {
					t.Fatalf("repeat/patch results: %v", counts)
				}
			} else if counts[200] != 1 || counts[409] != 1 {
				t.Fatalf("binding results: %v", counts)
			}
			if mode == "same-binding" && !bytes.Equal(replies[0].body, replies[1].body) {
				t.Fatal("identical PUTs returned different results")
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM dishes WHERE establishment_id=$1 AND external_id IS NOT NULL", f.venueID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("saved bindings=%d", count)
			}
			if mode == "patch" {
				d := f.readDish(t, f.dishID)
				if d.PriceMinor != 777 || d.CategoryID != category || d.ExternalID == nil || *d.ExternalID != "RACE" {
					t.Fatal("patch or binding lost")
				}
			}
		})
	}
}

func TestExternalDishIDSQLConstraintsAndCancellation(t *testing.T) {
	f := newOrderFixture(t)
	other := newOrderFixture(t)
	check := func(query string, args []any, code string) {
		t.Helper()
		_, err := f.pool.Exec(t.Context(), query, args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("constraint: %v, want %s", err, code)
		}
	}
	check("UPDATE dishes SET establishment_id=$1 WHERE id=$2", []any{other.venueID, f.dishID}, "23503")
	for _, bad := range []string{"bad\n", "bad ", "товар", strings.Repeat("x", 129), ""} {
		check("UPDATE dishes SET external_id=$1 WHERE id=$2", []any{bad, f.dishID}, "23514")
	}
	f.bind(t, f.dishID, "TAKEN", 200)
	unbound := decodeDish(t, f.menuRequest(t, http.MethodPost, "/api/v1/partner/dishes", f.dishPayload(t), 201))
	check("UPDATE dishes SET external_id='TAKEN' WHERE id=$1", []any{unbound.ID}, "23505")
	// Hold a real row lock: timeout must not leave a half-assigned identifier.
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(t.Context(), "SELECT id FROM dishes WHERE id=$1 FOR UPDATE", unbound.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	_, err = repository.New(f.pool).BindDishExternalID(ctx, f.venueID, unbound.ID, "WAITING")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline: %v", err)
	}
	if err = tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.readDish(t, unbound.ID).ExternalID != nil {
		t.Fatal("cancelled operation persisted")
	}
	f.bind(t, unbound.ID, "RECOVERED", 200)
}

func TestExternalDishIDMigration(t *testing.T) {
	pool := integrationPool(t, "EXTERNAL_ID_UPGRADE_DATABASE_URL")
	hash := sha256.Sum256([]byte(newTestKey()))
	var venue, category, dish, order int64
	for _, q := range []struct {
		sql  string
		args []any
		id   *int64
	}{
		{"INSERT INTO establishments(name,accepts_orders,api_key_hash) VALUES('External legacy',true,$1) RETURNING id", []any{hash[:]}, &venue},
	} {
		if err := pool.QueryRow(t.Context(), q.sql, q.args...).Scan(q.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(t.Context(), "INSERT INTO menu_categories(establishment_id,name) VALUES($1,'Legacy') RETURNING id", venue).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "INSERT INTO dishes(category_id,name,price_minor,is_available) VALUES($1,'Old dish',100,true) RETURNING id", category).Scan(&dish); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "INSERT INTO orders(establishment_id,tracking_token_hash,status,customer_name,customer_phone,delivery_address,total_minor) VALUES($1,$2,'confirmed','Old','12345','Address',100) RETURNING id", venue, hash[:]).Scan(&order); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO order_items(order_id,dish_id,dish_name,unit_price_minor,quantity) VALUES($1,$2,'Old dish',100,1)", order, dish); err != nil {
		t.Fatal(err)
	}
	migrate := func(args ...string) {
		t.Helper()
		goose := os.Getenv("TEST_GOOSE")
		if goose == "" {
			t.Fatal("TEST_GOOSE required")
		}
		cmd := exec.CommandContext(t.Context(), goose, append([]string{"-dir", filepath.Join("..", "..", "migrations"), "postgres", os.Getenv("EXTERNAL_ID_UPGRADE_DATABASE_URL")}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("migration %v: %v: %s", args, err, out)
		}
	}
	migrate("up-to", "11")
	repo := repository.New(pool)
	got, err := repo.GetDish(t.Context(), venue, dish)
	if err != nil || got.ExternalID != nil || got.Name != "Old dish" {
		t.Fatalf("legacy dish: %+v %v", got, err)
	}
	if _, err = repo.GetMenu(t.Context(), venue); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BindDishExternalID(t.Context(), venue, dish, "MIGRATED"); err != nil {
		t.Fatal(err)
	}
	before, err := repo.GetOrderByTrackingTokenHash(t.Context(), hash[:])
	if err != nil || len(before.Items) != 1 {
		t.Fatalf("legacy order: %v", err)
	}
	migrate("down-to", "10")
	var count int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM dishes d JOIN order_items i ON i.dish_id=d.id WHERE d.id=$1 AND i.order_id=$2", dish, order).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback lost data: %v", err)
	}
	migrate("up-to", "11")
	got, err = repo.GetDish(t.Context(), venue, dish)
	if err != nil || got.ExternalID != nil {
		t.Fatal("down/up did not remove only external mapping")
	}
	after, err := repo.GetOrderByTrackingTokenHash(t.Context(), hash[:])
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("order changed after down/up: %v", err)
	}
}
