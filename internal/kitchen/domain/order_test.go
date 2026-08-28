package domain

import "testing"

func TestOrderStatusCanTransitionTo(t *testing.T) {
	t.Parallel()

	allowed := map[OrderStatus][]OrderStatus{
		OrderStatusPendingConfirmation: {
			OrderStatusConfirmed,
			OrderStatusRejected,
			OrderStatusCancelled,
		},
		OrderStatusConfirmed:        {OrderStatusPreparing},
		OrderStatusPreparing:        {OrderStatusReadyForDelivery},
		OrderStatusReadyForDelivery: {OrderStatusDelivering},
		OrderStatusDelivering:       {OrderStatusCompleted},
	}

	statuses := []OrderStatus{
		OrderStatusPendingConfirmation,
		OrderStatusConfirmed,
		OrderStatusPreparing,
		OrderStatusReadyForDelivery,
		OrderStatusDelivering,
		OrderStatusCompleted,
		OrderStatusRejected,
		OrderStatusCancelled,
	}

	for _, current := range statuses {
		for _, next := range statuses {
			want := containsStatus(allowed[current], next)
			if got := current.CanTransitionTo(next); got != want {
				t.Errorf("CanTransitionTo(%q, %q) = %t, want %t", current, next, got, want)
			}
		}
	}
}

func TestOrderStatusIsTerminal(t *testing.T) {
	t.Parallel()

	tests := map[OrderStatus]bool{
		OrderStatusPendingConfirmation: false,
		OrderStatusConfirmed:           false,
		OrderStatusPreparing:           false,
		OrderStatusReadyForDelivery:    false,
		OrderStatusDelivering:          false,
		OrderStatusCompleted:           true,
		OrderStatusRejected:            true,
		OrderStatusCancelled:           true,
	}

	for status, want := range tests {
		if got := status.IsTerminal(); got != want {
			t.Errorf("IsTerminal(%q) = %t, want %t", status, got, want)
		}
	}
}

func containsStatus(statuses []OrderStatus, target OrderStatus) bool {
	for _, status := range statuses {
		if status == target {
			return true
		}
	}

	return false
}
