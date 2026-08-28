package service

import (
	"errors"
	"testing"

	"kitchen-api/internal/kitchen/domain"
)

func TestNormalizeName(t *testing.T) {
	t.Parallel()

	name, err := normalizeName("  Бургеры  ", 100)
	if err != nil {
		t.Fatalf("normalizeName() error = %v", err)
	}
	if name != "Бургеры" {
		t.Errorf("normalizeName() = %q, want %q", name, "Бургеры")
	}
}

func TestNormalizeNameRejectsEmptyName(t *testing.T) {
	t.Parallel()

	_, err := normalizeName("   ", 100)
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("normalizeName() error = %v, want ErrInvalidInput", err)
	}
}
