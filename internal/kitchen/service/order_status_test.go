package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"kitchen-api/internal/kitchen/domain"
)

// Unused operations deliberately panic via the embedded nil interface.
type statusRepository struct {
	orderRepository
	get    func() (domain.Order, error)
	update func(domain.OrderStatus, domain.OrderStatus) (domain.Order, error)
}

func (r statusRepository) GetOrder(context.Context, int64, int64) (domain.Order, error) {
	return r.get()
}
func (r statusRepository) UpdateOrderStatus(_ context.Context, _, _ int64, from, to domain.OrderStatus, _ *domain.RejectionReason, _ *string, _ time.Duration) (domain.Order, error) {
	return r.update(from, to)
}

func TestPartnerStatusConflictAndRepeat(t *testing.T) {
	reason := domain.RejectionReasonOther
	comment := "Original cause"
	saved := domain.Order{ID: 42, EstablishmentID: 1, Status: domain.OrderStatusRejected, RejectionReason: &reason, RejectionComment: &comment, UpdatedAt: time.Unix(100, 0)}
	requested := domain.RejectionReasonOverloaded
	failure := errors.New("repository unavailable")
	for _, tt := range []struct {
		name                         string
		initial                      domain.Order
		getErr, updateErr, reloadErr error
		latest                       domain.Order
		wantErr                      error
		writes                       int
	}{
		{name: "repeat retains original reason", initial: saved, latest: saved},
		{name: "competing rejection retains winner", initial: domain.Order{Status: domain.OrderStatusReadyForDelivery}, updateErr: domain.ErrConflict, latest: saved, writes: 1},
		{name: "delivery wins", initial: domain.Order{Status: domain.OrderStatusReadyForDelivery}, updateErr: domain.ErrConflict, latest: domain.Order{Status: domain.OrderStatusDelivering}, wantErr: domain.ErrInvalidStatusTransition, writes: 1},
		{name: "no automatic retry from new eligible status", initial: domain.Order{Status: domain.OrderStatusConfirmed}, updateErr: domain.ErrConflict, latest: domain.Order{Status: domain.OrderStatusPreparing}, wantErr: domain.ErrInvalidStatusTransition, writes: 1},
		{name: "initial read failure", getErr: failure, wantErr: failure},
		{name: "update failure", initial: domain.Order{Status: domain.OrderStatusConfirmed}, updateErr: failure, wantErr: failure, writes: 1},
		{name: "reload failure", initial: domain.Order{Status: domain.OrderStatusConfirmed}, updateErr: domain.ErrConflict, reloadErr: failure, wantErr: failure, writes: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads, writes := 0, 0
			repo := statusRepository{
				get: func() (domain.Order, error) {
					reads++
					if reads == 1 {
						return tt.initial, tt.getErr
					}
					return tt.latest, tt.reloadErr
				},
				update: func(from, to domain.OrderStatus) (domain.Order, error) {
					writes++
					if from != tt.initial.Status || to != domain.OrderStatusRejected {
						t.Fatalf("unexpected transition %s -> %s", from, to)
					}
					return domain.Order{}, tt.updateErr
				},
			}
			got, err := NewOrderService(repo, time.Minute).UpdatePartnerStatus(t.Context(), 1, 42, domain.OrderStatusRejected, &requested, nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && !reflect.DeepEqual(got, saved) {
				t.Fatalf("saved rejection changed: %+v", got)
			}
			if writes != tt.writes {
				t.Fatalf("writes = %d, want %d", writes, tt.writes)
			}
		})
	}
}

func TestPartnerRejectionValidationPrecedesRepeat(t *testing.T) {
	other, timeout, invalid := domain.RejectionReasonOther, domain.RejectionReasonConfirmationTimeout, domain.RejectionReason("invalid")
	blank, long := " \t\n", strings.Repeat("я", 2001)
	for _, tt := range []struct {
		name    string
		reason  *domain.RejectionReason
		comment *string
	}{
		{"missing reason", nil, nil}, {"invalid reason", &invalid, nil}, {"system reason", &timeout, nil},
		{"other missing comment", &other, nil}, {"other blank comment", &other, &blank}, {"long comment", &other, &long},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A nil repository proves that even a read is not attempted for invalid input.
			_, err := NewOrderService(nil, time.Minute).UpdatePartnerStatus(t.Context(), 1, 42, domain.OrderStatusRejected, tt.reason, tt.comment)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
