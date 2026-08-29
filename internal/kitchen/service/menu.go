package service

import (
	"context"
	"fmt"
	"strings"

	"kitchen-api/internal/kitchen/domain"
)

type menuRepository interface {
	GetMenu(context.Context, int64) (domain.Menu, error)
	CreateCategory(context.Context, int64, string) (domain.Category, error)
	UpdateCategory(context.Context, int64, int64, string) (domain.Category, error)
	CreateDish(context.Context, int64, domain.Dish) (domain.Dish, error)
	UpdateDish(context.Context, int64, int64, domain.DishPatch) (domain.Dish, error)
}

type MenuService struct {
	repository menuRepository
}

func NewMenuService(repository menuRepository) *MenuService {
	return &MenuService{repository: repository}
}

type NewDish struct {
	CategoryID  int64
	Name        string
	Description string
	PriceMinor  int64
	IsAvailable bool
}

type DishPatch struct {
	CategoryID  *int64
	Name        *string
	Description *string
	PriceMinor  *int64
	IsAvailable *bool
}

func (s *MenuService) GetMenu(ctx context.Context, establishmentID int64) (domain.Menu, error) {
	menu, err := s.repository.GetMenu(ctx, establishmentID)
	if err != nil {
		return domain.Menu{}, fmt.Errorf("get menu: %w", err)
	}

	return menu, nil
}

func (s *MenuService) CreateCategory(
	ctx context.Context,
	establishmentID int64,
	name string,
) (domain.Category, error) {
	name, err := normalizeName(name, 100)
	if err != nil {
		return domain.Category{}, err
	}

	category, err := s.repository.CreateCategory(ctx, establishmentID, name)
	if err != nil {
		return domain.Category{}, fmt.Errorf("create category: %w", err)
	}

	return category, nil
}

func (s *MenuService) UpdateCategory(
	ctx context.Context,
	establishmentID int64,
	categoryID int64,
	name string,
) (domain.Category, error) {
	name, err := normalizeName(name, 100)
	if err != nil {
		return domain.Category{}, err
	}

	category, err := s.repository.UpdateCategory(ctx, establishmentID, categoryID, name)
	if err != nil {
		return domain.Category{}, fmt.Errorf("update category: %w", err)
	}

	return category, nil
}

func (s *MenuService) CreateDish(
	ctx context.Context,
	establishmentID int64,
	input NewDish,
) (domain.Dish, error) {
	name, err := normalizeName(input.Name, 200)
	if err != nil {
		return domain.Dish{}, err
	}
	if input.CategoryID < 1 || input.PriceMinor < 0 || len([]rune(input.Description)) > 2000 {
		return domain.Dish{}, domain.ErrInvalidInput
	}

	dish, err := s.repository.CreateDish(ctx, establishmentID, domain.Dish{
		CategoryID:  input.CategoryID,
		Name:        name,
		Description: input.Description,
		PriceMinor:  input.PriceMinor,
		IsAvailable: input.IsAvailable,
	})
	if err != nil {
		return domain.Dish{}, fmt.Errorf("create dish: %w", err)
	}

	return dish, nil
}

func (s *MenuService) UpdateDish(
	ctx context.Context,
	establishmentID int64,
	dishID int64,
	patch DishPatch,
) (domain.Dish, error) {
	if patch.CategoryID == nil && patch.Name == nil && patch.Description == nil &&
		patch.PriceMinor == nil && patch.IsAvailable == nil {
		return domain.Dish{}, domain.ErrInvalidInput
	}

	if patch.CategoryID != nil {
		if *patch.CategoryID < 1 {
			return domain.Dish{}, domain.ErrInvalidInput
		}
	}
	if patch.Name != nil {
		name, err := normalizeName(*patch.Name, 200)
		if err != nil {
			return domain.Dish{}, err
		}
		patch.Name = &name
	}
	if patch.Description != nil {
		if len([]rune(*patch.Description)) > 2000 {
			return domain.Dish{}, domain.ErrInvalidInput
		}
	}
	if patch.PriceMinor != nil {
		if *patch.PriceMinor < 0 {
			return domain.Dish{}, domain.ErrInvalidInput
		}
	}

	updated, err := s.repository.UpdateDish(ctx, establishmentID, dishID, domain.DishPatch{
		CategoryID:  patch.CategoryID,
		Name:        patch.Name,
		Description: patch.Description,
		PriceMinor:  patch.PriceMinor,
		IsAvailable: patch.IsAvailable,
	})
	if err != nil {
		return domain.Dish{}, fmt.Errorf("update dish: %w", err)
	}

	return updated, nil
}

func normalizeName(name string, maxLength int) (string, error) {
	name = strings.TrimSpace(name)
	length := len([]rune(name))
	if length == 0 || length > maxLength {
		return "", domain.ErrInvalidInput
	}

	return name, nil
}
