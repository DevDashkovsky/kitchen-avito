package service

import (
	"context"
	"fmt"

	"kitchen-api/internal/kitchen/domain"
)

func validExternalDishID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}

func (s *MenuService) GetDish(ctx context.Context, establishmentID, dishID int64) (domain.Dish, error) {
	if dishID < 1 {
		return domain.Dish{}, domain.ErrInvalidInput
	}
	dish, err := s.repository.GetDish(ctx, establishmentID, dishID)
	if err != nil {
		return domain.Dish{}, fmt.Errorf("get partner dish: %w", err)
	}
	return dish, nil
}
func (s *MenuService) GetDishByExternalID(ctx context.Context, establishmentID int64, externalID string) (domain.Dish, error) {
	if !validExternalDishID(externalID) {
		return domain.Dish{}, domain.ErrInvalidInput
	}
	dish, err := s.repository.GetDishByExternalID(ctx, establishmentID, externalID)
	if err != nil {
		return domain.Dish{}, fmt.Errorf("find partner dish: %w", err)
	}
	return dish, nil
}
func (s *MenuService) BindDishExternalID(ctx context.Context, establishmentID, dishID int64, externalID string) (domain.Dish, error) {
	if dishID < 1 || !validExternalDishID(externalID) {
		return domain.Dish{}, domain.ErrInvalidInput
	}
	dish, err := s.repository.BindDishExternalID(ctx, establishmentID, dishID, externalID)
	if err != nil {
		return domain.Dish{}, fmt.Errorf("bind dish external ID: %w", err)
	}
	return dish, nil
}
