package domain

import "testing"

func TestOrderStatusCanTransitionTo(t *testing.T) {
	t.Parallel()

	statuses := []OrderStatus{OrderStatusPendingConfirmation, OrderStatusConfirmed,
		OrderStatusPreparing, OrderStatusReadyForDelivery, OrderStatusDelivering,
		OrderStatusCompleted, OrderStatusRejected, OrderStatusCancelled, "invalid"}
	allowed := map[OrderStatus][]OrderStatus{
		OrderStatusPendingConfirmation: {OrderStatusConfirmed, OrderStatusRejected, OrderStatusCancelled},
		OrderStatusConfirmed:           {OrderStatusPreparing, OrderStatusRejected},
		OrderStatusPreparing:           {OrderStatusReadyForDelivery, OrderStatusRejected},
		OrderStatusReadyForDelivery:    {OrderStatusDelivering, OrderStatusRejected},
		OrderStatusDelivering:          {OrderStatusCompleted},
	}
	for _, current := range statuses {
		for _, next := range statuses {
			want := false
			for _, target := range allowed[current] {
				want = want || next == target
			}
			if got := current.CanTransitionTo(next); got != want {
				t.Errorf("CanTransitionTo(%q, %q) = %t, want %t", current, next, got, want)
			}
		}
	}
}

func TestOrderStatusIsTerminal(t *testing.T) {
	t.Parallel()

	terminal := []OrderStatus{
		OrderStatusCompleted,
		OrderStatusRejected,
		OrderStatusCancelled,
	}

	for _, status := range terminal {
		if !status.IsTerminal() {
			t.Errorf("IsTerminal(%q) = false, want true", status)
		}
	}

	if OrderStatusPreparing.IsTerminal() {
		t.Error("IsTerminal(preparing) = true, want false")
	}
}
