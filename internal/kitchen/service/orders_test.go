package service

import (
	"errors"
	"math"
	"testing"

	"kitchen-api/internal/kitchen/domain"
)

func TestCombineOrderItems(t *testing.T) {
	t.Parallel()

	items, dishIDs, err := combineOrderItems([]CreateOrderItem{
		{DishID: 10, Quantity: 1, ExpectedUnitPriceMinor: 45900},
		{DishID: 20, Quantity: 2, ExpectedUnitPriceMinor: 12900},
		{DishID: 10, Quantity: 3, ExpectedUnitPriceMinor: 45900},
	})
	if err != nil {
		t.Fatalf("combineOrderItems() error = %v", err)
	}

	if len(items) != 2 || len(dishIDs) != 2 {
		t.Fatalf("combined lengths = %d items and %d ids, want 2 and 2", len(items), len(dishIDs))
	}
	if items[0].DishID != 10 || items[0].Quantity != 4 {
		t.Errorf("first item = %+v, want dish 10 with quantity 4", items[0])
	}
	if dishIDs[0] != 10 || dishIDs[1] != 20 {
		t.Errorf("dish ids = %v, want [10 20]", dishIDs)
	}
}

func TestCombineOrderItemsRejectsInvalidDuplicates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []CreateOrderItem
	}{
		{
			name: "different prices",
			items: []CreateOrderItem{
				{DishID: 10, Quantity: 1, ExpectedUnitPriceMinor: 45900},
				{DishID: 10, Quantity: 1, ExpectedUnitPriceMinor: 49900},
			},
		},
		{
			name: "quantity overflow",
			items: []CreateOrderItem{
				{DishID: 10, Quantity: math.MaxInt32, ExpectedUnitPriceMinor: 45900},
				{DishID: 10, Quantity: 1, ExpectedUnitPriceMinor: 45900},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := combineOrderItems(test.items)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("combineOrderItems() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeCreateOrderInput(t *testing.T) {
	t.Parallel()

	input := CreateOrderInput{
		EstablishmentID:   1,
		TrackingTokenHash: []byte("hash"),
		CustomerName:      "  Иван  ",
		CustomerPhone:     "  +79990000000  ",
		DeliveryAddress:   "  Ленина, 1  ",
		Items: []CreateOrderItem{
			{DishID: 1, Quantity: 1, ExpectedUnitPriceMinor: 100},
		},
	}

	got, err := normalizeCreateOrderInput(input)
	if err != nil {
		t.Fatalf("normalizeCreateOrderInput() error = %v", err)
	}
	if got.CustomerName != "Иван" || got.CustomerPhone != "+79990000000" ||
		got.DeliveryAddress != "Ленина, 1" {
		t.Errorf("normalized contacts = %q, %q, %q", got.CustomerName, got.CustomerPhone, got.DeliveryAddress)
	}
}

func TestValidatePartnerStatusUpdate(t *testing.T) {
	t.Parallel()

	other := domain.RejectionReasonOther
	timeout := domain.RejectionReasonConfirmationTimeout
	comment := "нет нужных продуктов"

	tests := []struct {
		name    string
		status  domain.OrderStatus
		reason  *domain.RejectionReason
		comment *string
		wantErr bool
	}{
		{name: "confirm", status: domain.OrderStatusConfirmed},
		{name: "reject with comment", status: domain.OrderStatusRejected, reason: &other, comment: &comment},
		{name: "other without comment", status: domain.OrderStatusRejected, reason: &other, wantErr: true},
		{name: "partner timeout", status: domain.OrderStatusRejected, reason: &timeout, wantErr: true},
		{name: "partner cancellation", status: domain.OrderStatusCancelled, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := validatePartnerStatusUpdate(test.status, test.reason, test.comment)
			if (err != nil) != test.wantErr {
				t.Fatalf("validatePartnerStatusUpdate() error = %v, wantErr = %t", err, test.wantErr)
			}
		})
	}
}
