package domain

import "testing"

func TestOrderStatusCanTransitionTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		current OrderStatus
		next    OrderStatus
		want    bool
	}{
		{OrderStatusPendingConfirmation, OrderStatusConfirmed, true},
		{OrderStatusPendingConfirmation, OrderStatusRejected, true},
		{OrderStatusPendingConfirmation, OrderStatusCancelled, true},
		{OrderStatusConfirmed, OrderStatusPreparing, true},
		{OrderStatusPreparing, OrderStatusReadyForDelivery, true},
		{OrderStatusReadyForDelivery, OrderStatusDelivering, true},
		{OrderStatusDelivering, OrderStatusCompleted, true},
		{OrderStatusPendingConfirmation, OrderStatusPreparing, false},
		{OrderStatusConfirmed, OrderStatusCompleted, false},
		{OrderStatusCompleted, OrderStatusPreparing, false},
		{OrderStatusRejected, OrderStatusConfirmed, false},
		{OrderStatusCancelled, OrderStatusConfirmed, false},
	}

	for _, test := range tests {
		if got := test.current.CanTransitionTo(test.next); got != test.want {
			t.Errorf("CanTransitionTo(%q, %q) = %t, want %t", test.current, test.next, got, test.want)
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
