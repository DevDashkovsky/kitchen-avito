package domain

import "errors"

var (
	ErrIdempotencyKeyReused    = errors.New("idempotency key reused")
	ErrInvalidInput            = errors.New("invalid input")
	ErrUnauthorized            = errors.New("unauthorized")
	ErrNotFound                = errors.New("not found")
	ErrConflict                = errors.New("conflict")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrCancellationNotAllowed  = errors.New("cancellation not allowed")
)

// MenuChangedError carries the menu state observed while the order rows were
// locked. It lets the application return precise conflict details instead of
// losing them in the read-before-write race window.
type MenuChangedError struct {
	Dishes []Dish
}

func (e *MenuChangedError) Error() string {
	return "menu changed while creating order"
}

func (e *MenuChangedError) Unwrap() error {
	return ErrConflict
}
