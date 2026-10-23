package domain

import "time"

type OrderStatus string

const (
	OrderStatusPendingConfirmation OrderStatus = "pending_confirmation"
	OrderStatusConfirmed           OrderStatus = "confirmed"
	OrderStatusPreparing           OrderStatus = "preparing"
	OrderStatusReadyForDelivery    OrderStatus = "ready_for_delivery"
	OrderStatusDelivering          OrderStatus = "delivering"
	OrderStatusCompleted           OrderStatus = "completed"
	OrderStatusRejected            OrderStatus = "rejected"
	OrderStatusCancelled           OrderStatus = "cancelled"
)

func (s OrderStatus) IsValid() bool {
	switch s {
	case OrderStatusPendingConfirmation,
		OrderStatusConfirmed,
		OrderStatusPreparing,
		OrderStatusReadyForDelivery,
		OrderStatusDelivering,
		OrderStatusCompleted,
		OrderStatusRejected,
		OrderStatusCancelled:
		return true
	default:
		return false
	}
}

func (s OrderStatus) IsTerminal() bool {
	return s == OrderStatusCompleted || s == OrderStatusRejected || s == OrderStatusCancelled
}

func (s OrderStatus) CanTransitionTo(next OrderStatus) bool {
	switch s {
	case OrderStatusPendingConfirmation:
		return next == OrderStatusConfirmed || next == OrderStatusRejected || next == OrderStatusCancelled
	case OrderStatusConfirmed:
		return next == OrderStatusPreparing
	case OrderStatusPreparing:
		return next == OrderStatusReadyForDelivery
	case OrderStatusReadyForDelivery:
		return next == OrderStatusDelivering
	case OrderStatusDelivering:
		return next == OrderStatusCompleted
	default:
		return false
	}
}

type RejectionReason string

const (
	RejectionReasonOutOfStock          RejectionReason = "out_of_stock"
	RejectionReasonOverloaded          RejectionReason = "overloaded"
	RejectionReasonDeliveryUnavailable RejectionReason = "delivery_unavailable"
	RejectionReasonOther               RejectionReason = "other"
	RejectionReasonConfirmationTimeout RejectionReason = "confirmation_timeout"
)

func (r RejectionReason) IsValid() bool {
	switch r {
	case RejectionReasonOutOfStock,
		RejectionReasonOverloaded,
		RejectionReasonDeliveryUnavailable,
		RejectionReasonOther,
		RejectionReasonConfirmationTimeout:
		return true
	default:
		return false
	}
}

type Order struct {
	ID                 int64
	EstablishmentID    int64
	TrackingTokenHash  []byte
	RequestFingerprint []byte
	Status             OrderStatus
	CustomerName       string
	CustomerPhone      string
	DeliveryAddress    string
	Comment            *string
	TotalMinor         int64
	RejectionReason    *RejectionReason
	RejectionComment   *string
	Items              []OrderItem
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type OrderItem struct {
	ID             int64
	OrderID        int64
	DishID         int64
	DishName       string
	UnitPriceMinor int64
	Quantity       int32
}
