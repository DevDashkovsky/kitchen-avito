package service

import (
	"errors"
	"strings"
	"testing"

	"kitchen-api/internal/kitchen/domain"
)

func TestExternalDishIDFormat(t *testing.T) {
	for _, value := range []string{"0", "00001", "SKU-1", "sku-1", "a.Z_9:-", strings.Repeat("A", 128)} {
		if !validExternalDishID(value) {
			t.Errorf("valid ID rejected: %q", value)
		}
	}
	for _, value := range []string{"", strings.Repeat("A", 129), " SKU", "SKU ", "SKU\n", "a\tb", "a/b", "a+b", "товар", "café", "Ａ", "\x00"} {
		t.Run(value, func(t *testing.T) {
			if validExternalDishID(value) {
				t.Fatalf("invalid ID accepted: %q", value)
			}
			svc := NewMenuService(nil)
			if _, err := svc.CreateDish(t.Context(), 1, NewDish{ExternalID: &value}); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("create: %v", err)
			}
			if _, err := svc.GetDishByExternalID(t.Context(), 1, value); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("get: %v", err)
			}
			if _, err := svc.BindDishExternalID(t.Context(), 1, 1, value); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("bind: %v", err)
			}
		})
	}
}
