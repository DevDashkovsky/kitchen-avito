//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
	"kitchen-api/internal/kitchen/repository"
)

type statusHTTPResult struct {
	request *http.Request
	status  int
	header  http.Header
	body    []byte
	err     error
}

// No testing.Fatal here: requests also run in racing goroutines.
func (f *orderFixture) statusRequest(ctx context.Context, method, path string, body any, header, secret string) statusHTTPResult {
	var r statusHTTPResult
	var data []byte
	if body != nil {
		data, r.err = json.Marshal(body)
		if r.err != nil {
			return r
		}
	}
	r.request, r.err = http.NewRequestWithContext(ctx, method, f.server.URL+path, bytes.NewReader(data))
	if r.err != nil {
		return r
	}
	if body != nil {
		r.request.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		r.request.Header.Set(header, secret)
	}
	res, err := f.server.Client().Do(r.request)
	if err != nil {
		r.err = err
		return r
	}
	defer func() { _ = res.Body.Close() }()
	r.status, r.header = res.StatusCode, res.Header
	r.body, r.err = io.ReadAll(res.Body)
	return r
}

func (f *orderFixture) checkStatusResponse(t *testing.T, r statusHTTPResult, want int) {
	t.Helper()
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.status != want {
		t.Fatalf("%s %s: status %d, want %d: %s", r.request.Method, r.request.URL.Path, r.status, want, r.body)
	}
	router, err := legacy.NewRouter(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	route, params, err := router.FindRoute(r.request)
	if err != nil {
		t.Fatal(err)
	}
	input := &openapi3filter.RequestValidationInput{Request: r.request, PathParams: params, Route: route}
	output := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: r.status, Header: r.header, Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	if err := openapi3filter.ValidateResponse(t.Context(), output.SetBodyBytes(r.body)); err != nil {
		t.Fatalf("OpenAPI response: %v", err)
	}
}

func statusPath(id int64) string { return fmt.Sprintf("/api/v1/partner/orders/%d/status", id) }
func rejectionBody(reason string) map[string]string {
	return map[string]string{"status": "rejected", "rejection_reason": reason, "rejection_comment": "Kitchen cannot fulfil this order"}
}
func (f *orderFixture) patchStatus(t *testing.T, id int64, body any, want int) statusHTTPResult {
	t.Helper()
	r := f.statusRequest(t.Context(), http.MethodPatch, statusPath(id), body, "Authorization", "Bearer "+f.partnerKey)
	f.checkStatusResponse(t, r, want)
	return r
}

func (f *orderFixture) orderAtStatus(t *testing.T, status string) (int64, string, []byte) {
	t.Helper()
	key := newTestKey()
	code, receipt := f.post(t, key, f.body)
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	hash := sha256.Sum256([]byte(key))
	var id int64
	if err := f.pool.QueryRow(t.Context(), "SELECT id FROM orders WHERE tracking_token_hash=$1", hash[:]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if status == "pending_confirmation" {
		return id, key, receipt
	}
	if status == "cancelled" {
		r := f.statusRequest(t.Context(), http.MethodPost, "/api/v1/orders/cancel", nil, "X-Tracking-Token", key)
		f.checkStatusResponse(t, r, 200)
		return id, key, receipt
	}
	for _, next := range []string{"confirmed", "preparing", "ready_for_delivery", "delivering", "completed"} {
		f.patchStatus(t, id, map[string]string{"status": next}, 200)
		if next == status {
			return id, key, receipt
		}
	}
	t.Fatalf("unknown fixture status %s", status)
	return 0, "", nil
}

func (f *orderFixture) storedOrder(t *testing.T, id int64) domain.Order {
	t.Helper()
	order, err := repository.New(f.pool).GetOrder(t.Context(), f.venueID, id)
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func TestLateRejectionHTTP(t *testing.T) {
	for _, from := range []string{"confirmed", "preparing", "ready_for_delivery"} {
		t.Run(from, func(t *testing.T) {
			f := newOrderFixture(t)
			id, key, receipt := f.orderAtStatus(t, from)
			before := f.storedOrder(t, id)
			first := f.patchStatus(t, id, rejectionBody("other"), 200)
			saved := f.storedOrder(t, id)
			if saved.Status != domain.OrderStatusRejected || saved.RejectionReason == nil || *saved.RejectionReason != domain.RejectionReasonOther || saved.RejectionComment == nil {
				t.Fatalf("rejection not persisted: %+v", saved)
			}
			if saved.TotalMinor != before.TotalMinor || !reflect.DeepEqual(saved.Items, before.Items) || !saved.CreatedAt.Equal(before.CreatedAt) || !bytes.Equal(saved.TrackingTokenHash, before.TrackingTokenHash) || !bytes.Equal(saved.RequestFingerprint, before.RequestFingerprint) {
				t.Fatal("rejection changed order contents or credentials")
			}
			if !saved.UpdatedAt.After(before.UpdatedAt) {
				t.Fatal("successful update did not advance updated_at")
			}
			repeat := f.patchStatus(t, id, rejectionBody("overloaded"), 200)
			if !bytes.Equal(first.body, repeat.body) || !reflect.DeepEqual(saved, f.storedOrder(t, id)) {
				t.Fatal("repeat changed saved rejection")
			}
			f.patchStatus(t, id, map[string]string{"status": "preparing"}, 409)
			tracked := f.statusRequest(t.Context(), http.MethodGet, "/api/v1/orders/status", nil, "X-Tracking-Token", key)
			f.checkStatusResponse(t, tracked, 200)
			var got api.TrackedOrder
			if err := json.Unmarshal(tracked.body, &got); err != nil {
				t.Fatal(err)
			}
			if string(got.Status) != "rejected" || got.RejectionReason == nil || string(*got.RejectionReason) != "other" || got.RejectionComment == nil || *got.RejectionComment != *saved.RejectionComment || got.TotalMinor != before.TotalMinor {
				t.Fatalf("customer does not see rejection: %s", tracked.body)
			}
			code, replay := f.post(t, key, f.body)
			if code != 200 || !bytes.Equal(receipt, replay) {
				t.Fatal("rejection invalidated creation receipt")
			}
		})
	}
}

func TestLateRejectionValidationAndAccess(t *testing.T) {
	f := newOrderFixture(t)
	id, _, _ := f.orderAtStatus(t, "confirmed")
	before := f.storedOrder(t, id)
	for _, body := range []any{
		map[string]string{"status": "rejected"}, rejectionBody("confirmation_timeout"), rejectionBody("invalid"),
		map[string]string{"status": "rejected", "rejection_reason": "other"},
		map[string]string{"status": "rejected", "rejection_reason": "other", "rejection_comment": " \t\n"},
		map[string]string{"status": "rejected", "rejection_reason": "other", "rejection_comment": strings.Repeat("я", 2001)},
	} {
		f.patchStatus(t, id, body, 400)
	}
	for _, secret := range []string{"", "Bearer wrong"} {
		r := f.statusRequest(t.Context(), http.MethodPatch, statusPath(id), rejectionBody("other"), "Authorization", secret)
		f.checkStatusResponse(t, r, 401)
	}
	foreign := newOrderFixture(t)
	r := f.statusRequest(t.Context(), http.MethodPatch, statusPath(id), rejectionBody("other"), "Authorization", "Bearer "+foreign.partnerKey)
	f.checkStatusResponse(t, r, 404)
	missing := f.patchStatus(t, 9223372036854775807, rejectionBody("other"), 404)
	if !bytes.Equal(r.body, missing.body) {
		t.Fatal("foreign order existence disclosed")
	}
	read := f.statusRequest(t.Context(), http.MethodGet, fmt.Sprintf("/api/v1/partner/orders/%d", id), nil, "Authorization", "Bearer "+foreign.partnerKey)
	f.checkStatusResponse(t, read, 404)
	if !reflect.DeepEqual(before, f.storedOrder(t, id)) {
		t.Fatal("invalid or unauthorized request changed order")
	}
	f.patchStatus(t, id, rejectionBody("other"), 200)
	f.patchStatus(t, id, map[string]string{"status": "rejected"}, 400)
	for _, reason := range []string{"out_of_stock", "overloaded", "delivery_unavailable"} {
		orderID, _, _ := f.orderAtStatus(t, "preparing")
		f.patchStatus(t, orderID, map[string]string{"status": "rejected", "rejection_reason": reason}, 200)
		saved := f.storedOrder(t, orderID)
		if saved.RejectionReason == nil || string(*saved.RejectionReason) != reason {
			t.Fatalf("reason not saved: %s", reason)
		}
	}
	for _, from := range []string{"delivering", "completed", "cancelled"} {
		t.Run(from, func(t *testing.T) {
			orderID, _, _ := f.orderAtStatus(t, from)
			old := f.storedOrder(t, orderID)
			response := f.patchStatus(t, orderID, rejectionBody("other"), 409)
			if !bytes.Contains(response.body, []byte("invalid_status_transition")) {
				t.Fatalf("wrong conflict: %s", response.body)
			}
			if !reflect.DeepEqual(old, f.storedOrder(t, orderID)) {
				t.Fatal("forbidden rejection changed order")
			}
		})
	}
}

func TestLateRejectionTimeoutAndCancellation(t *testing.T) {
	f := newOrderFixture(t)
	id, key, _ := f.orderAtStatus(t, "confirmed")
	cancelled := f.statusRequest(t.Context(), http.MethodPost, "/api/v1/orders/cancel", nil, "X-Tracking-Token", key)
	f.checkStatusResponse(t, cancelled, 409)
	if _, err := f.pool.Exec(t.Context(), "UPDATE orders SET created_at=NOW()-INTERVAL '1 day' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	f.patchStatus(t, id, rejectionBody("out_of_stock"), 200)
	id, _, _ = f.orderAtStatus(t, "pending_confirmation")
	f.patchStatus(t, id, rejectionBody("overloaded"), 200)
	id, _, _ = f.orderAtStatus(t, "pending_confirmation")
	if _, err := f.pool.Exec(t.Context(), "UPDATE orders SET created_at=NOW()-INTERVAL '1 day' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	f.patchStatus(t, id, rejectionBody("other"), 409)
	saved := f.storedOrder(t, id)
	if saved.RejectionReason == nil || *saved.RejectionReason != domain.RejectionReasonConfirmationTimeout {
		t.Fatal("timeout reason lost")
	}
	f.patchStatus(t, id, rejectionBody("other"), 200)
	if !reflect.DeepEqual(saved, f.storedOrder(t, id)) {
		t.Fatal("retry overwrote timeout")
	}
}

func TestLateRejectionHTTPRaces(t *testing.T) {
	for _, competing := range []string{"delivering", "rejected"} {
		t.Run(competing, func(t *testing.T) {
			f := newOrderFixture(t)
			id, _, _ := f.orderAtStatus(t, "ready_for_delivery")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			start := make(chan struct{})
			results := make(chan statusHTTPResult, 2)
			rival := map[string]string{"status": "delivering"}
			if competing == "rejected" {
				rival = rejectionBody("overloaded")
			}
			for _, body := range []map[string]string{rejectionBody("other"), rival} {
				go func() {
					<-start
					results <- f.statusRequest(ctx, http.MethodPatch, statusPath(id), body, "Authorization", "Bearer "+f.partnerKey)
				}()
			}
			close(start)
			replies := make([]statusHTTPResult, 0, 2)
			for range 2 {
				select {
				case r := <-results:
					replies = append(replies, r)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			counts := map[int]int{}
			for _, r := range replies {
				if r.status != 200 && r.status != 409 {
					t.Fatalf("unexpected race result: %d %v %s", r.status, r.err, r.body)
				}
				f.checkStatusResponse(t, r, r.status)
				counts[r.status]++
			}
			saved := f.storedOrder(t, id)
			if competing == "rejected" {
				if counts[200] != 2 || !bytes.Equal(replies[0].body, replies[1].body) || saved.Status != domain.OrderStatusRejected {
					t.Fatalf("competing refusals: %v", counts)
				}
			} else {
				if counts[200] != 1 || counts[409] != 1 {
					t.Fatalf("delivery race: %v", counts)
				}
				if saved.Status != domain.OrderStatusRejected && saved.Status != domain.OrderStatusDelivering {
					t.Fatalf("unexpected winner: %s", saved.Status)
				}
			}
			for _, r := range replies {
				if r.status == 200 {
					var got api.UpdateOrderStatusResponse
					if err := json.Unmarshal(r.body, &got); err != nil {
						t.Fatal(err)
					}
					if string(got.Status) != string(saved.Status) || !got.UpdatedAt.Equal(saved.UpdatedAt) {
						t.Fatal("response differs from persisted winner")
					}
					if saved.RejectionReason != nil {
						if got.RejectionReason == nil || string(*got.RejectionReason) != string(*saved.RejectionReason) || got.RejectionComment == nil || *got.RejectionComment != *saved.RejectionComment {
							t.Fatal("mixed rejection fields")
						}
					} else if got.RejectionReason != nil || got.RejectionComment != nil {
						t.Fatal("delivery retained rejection fields")
					}
				}
			}
		})
	}
}

func TestLateRejectionConditionalSQL(t *testing.T) {
	f := newOrderFixture(t)
	id, _, _ := f.orderAtStatus(t, "ready_for_delivery")
	repo := repository.New(f.pool)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		order domain.Order
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	reason := domain.RejectionReasonOther
	comment := "No courier"
	for _, target := range []domain.OrderStatus{domain.OrderStatusRejected, domain.OrderStatusDelivering} {
		go func() {
			<-start
			var cause *domain.RejectionReason
			var detail *string
			if target == domain.OrderStatusRejected {
				cause, detail = &reason, &comment
			}
			order, err := repo.UpdateOrderStatus(ctx, f.venueID, id, domain.OrderStatusReadyForDelivery, target, cause, detail, time.Minute)
			results <- result{order, err}
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	var winner domain.Order
	for range 2 {
		select {
		case r := <-results:
			if r.err == nil {
				successes++
				winner = r.order
			} else if errors.Is(r.err, domain.ErrConflict) {
				conflicts++
			} else {
				t.Fatal(r.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	saved := f.storedOrder(t, id)
	if saved.Status != winner.Status || !reflect.DeepEqual(saved.RejectionReason, winner.RejectionReason) || !reflect.DeepEqual(saved.RejectionComment, winner.RejectionComment) || !saved.UpdatedAt.Equal(winner.UpdatedAt) {
		t.Fatal("SQL winner was overwritten")
	}
	// A correct status predicate alone must not permit another tenant to write.
	_, err := repo.UpdateOrderStatus(t.Context(), f.venueID+1, id, saved.Status, domain.OrderStatusRejected, &reason, &comment, time.Minute)
	if !errors.Is(err, domain.ErrConflict) || !reflect.DeepEqual(saved, f.storedOrder(t, id)) {
		t.Fatalf("tenant predicate failed: %v", err)
	}
}
