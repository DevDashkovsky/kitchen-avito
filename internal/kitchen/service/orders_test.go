package service

import (
	"testing"
	"time"

	"kitchen-api/internal/kitchen/domain"
)

func TestOrderCursorRoundTrip(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.September, 17, 10, 11, 12, 345, time.UTC)
	encoded, err := encodeOrderCursor(domain.Order{ID: 42, CreatedAt: createdAt})
	if err != nil {
		t.Fatalf("encodeOrderCursor() error = %v", err)
	}

	decoded, err := decodeOrderCursor(&encoded)
	if err != nil {
		t.Fatalf("decodeOrderCursor() error = %v", err)
	}
	if decoded.id == nil || *decoded.id != 42 || decoded.createdAt == nil || !decoded.createdAt.Equal(createdAt) {
		t.Fatalf("decoded cursor = %+v", decoded)
	}
}

func TestDecodeOrderCursorRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, cursor := range []string{"not-base64", "e30"} {
		cursor := cursor
		t.Run(cursor, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeOrderCursor(&cursor); err == nil {
				t.Fatal("decodeOrderCursor() error = nil, want error")
			}
		})
	}
}

func TestDishConflictsFromCurrentState(t *testing.T) {
	t.Parallel()

	conflicts := dishConflictsFromCurrentState(
		[]CreateOrderItem{
			{DishID: 1, ExpectedUnitPriceMinor: 100},
			{DishID: 2, ExpectedUnitPriceMinor: 200},
			{DishID: 3, ExpectedUnitPriceMinor: 300},
		},
		[]domain.Dish{
			{ID: 1, PriceMinor: 150, IsAvailable: true},
			{ID: 2, PriceMinor: 200, IsAvailable: false},
		},
	)

	if len(conflicts) != 3 {
		t.Fatalf("len(conflicts) = %d, want 3", len(conflicts))
	}
	if conflicts[0].DishID != 1 || conflicts[0].Reason != DishConflictPriceChanged ||
		conflicts[0].CurrentUnitPriceMinor == nil || *conflicts[0].CurrentUnitPriceMinor != 150 {
		t.Fatalf("price conflict = %+v", conflicts[0])
	}
	if conflicts[1].DishID != 2 || conflicts[1].Reason != DishConflictDishUnavailable {
		t.Fatalf("availability conflict = %+v", conflicts[1])
	}
	if conflicts[2].DishID != 3 || conflicts[2].Reason != DishConflictDishUnavailable {
		t.Fatalf("missing dish conflict = %+v", conflicts[2])
	}
}
