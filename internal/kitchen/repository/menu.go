package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"kitchen-api/internal/kitchen/domain"
)

const categoryColumns = `
    id,
    establishment_id,
    name,
    created_at,
    updated_at`

const dishColumns = `
    external_id,
    id,
    category_id,
    name,
    description,
    price_minor,
    is_available,
    created_at,
    updated_at`

func (r *Repository) GetMenu(ctx context.Context, establishmentID int64) (domain.Menu, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return domain.Menu{}, fmt.Errorf("begin menu snapshot: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	query := `SELECT ` + establishmentColumns + ` FROM establishments WHERE id = $1`
	establishment, err := scanEstablishment(tx.QueryRow(ctx, query, establishmentID))
	if err != nil {
		return domain.Menu{}, establishmentError("get menu establishment", err)
	}

	categories, err := listCategories(ctx, tx, establishmentID)
	if err != nil {
		return domain.Menu{}, err
	}

	dishes, err := listDishes(ctx, tx, establishmentID)
	if err != nil {
		return domain.Menu{}, err
	}

	categoryIndexes := make(map[int64]int, len(categories))
	menuCategories := make([]domain.MenuCategory, len(categories))
	for index, category := range categories {
		categoryIndexes[category.ID] = index
		menuCategories[index] = domain.MenuCategory{
			Category: category,
			Dishes:   make([]domain.Dish, 0),
		}
	}

	for _, dish := range dishes {
		index, ok := categoryIndexes[dish.CategoryID]
		if !ok {
			return domain.Menu{}, fmt.Errorf("build menu: dish %d has unknown category %d", dish.ID, dish.CategoryID)
		}

		menuCategories[index].Dishes = append(menuCategories[index].Dishes, dish)
	}

	menu := domain.Menu{
		Establishment: establishment,
		Categories:    menuCategories,
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Menu{}, fmt.Errorf("commit menu snapshot: %w", err)
	}

	return menu, nil
}

func (r *Repository) CreateCategory(
	ctx context.Context,
	establishmentID int64,
	name string,
) (domain.Category, error) {
	query := `
INSERT INTO menu_categories (establishment_id, name)
VALUES ($1, $2)
RETURNING ` + categoryColumns

	category, err := scanCategory(r.pool.QueryRow(ctx, query, establishmentID, name))
	if err != nil {
		return domain.Category{}, menuError("create category", err)
	}

	return category, nil
}

func (r *Repository) UpdateCategory(
	ctx context.Context,
	establishmentID int64,
	categoryID int64,
	name string,
) (domain.Category, error) {
	query := `
UPDATE menu_categories
SET name = $3,
    updated_at = NOW()
WHERE id = $2
  AND establishment_id = $1
RETURNING ` + categoryColumns

	category, err := scanCategory(r.pool.QueryRow(ctx, query, establishmentID, categoryID, name))
	if err != nil {
		return domain.Category{}, menuError("update category", err)
	}

	return category, nil
}

func (r *Repository) CreateDish(
	ctx context.Context,
	establishmentID int64,
	dish domain.Dish,
) (domain.Dish, error) {
	query := `
INSERT INTO dishes (
    establishment_id,
    external_id,
    category_id,
    name,
    description,
    price_minor,
    is_available
)
SELECT
    c.establishment_id,
    $7,
    c.id,
    $3,
    $4,
    $5,
    $6
FROM menu_categories AS c
WHERE c.id = $2
  AND c.establishment_id = $1
RETURNING ` + dishColumns

	created, err := scanDish(r.pool.QueryRow(
		ctx,
		query,
		establishmentID,
		dish.CategoryID,
		dish.Name,
		dish.Description,
		dish.PriceMinor,
		dish.IsAvailable,
		dish.ExternalID,
	))
	if err != nil {
		return domain.Dish{}, dishError("create dish", err)
	}

	return created, nil
}

func (r *Repository) UpdateDish(
	ctx context.Context,
	establishmentID int64,
	dishID int64,
	patch domain.DishPatch,
) (domain.Dish, error) {
	query := `
UPDATE dishes AS d
SET category_id = COALESCE($3::BIGINT, d.category_id),
    name = COALESCE($4::TEXT, d.name),
    description = COALESCE($5::TEXT, d.description),
    price_minor = COALESCE($6::BIGINT, d.price_minor),
    is_available = COALESCE($7::BOOLEAN, d.is_available),
    updated_at = NOW()
WHERE d.id = $2
  AND EXISTS (
      SELECT 1
      FROM menu_categories AS current_category
      WHERE current_category.id = d.category_id
        AND current_category.establishment_id = $1
  )
  AND EXISTS (
      SELECT 1
      FROM menu_categories AS new_category
      WHERE new_category.id = COALESCE($3::BIGINT, d.category_id)
        AND new_category.establishment_id = $1
  )
RETURNING ` + dishColumns

	updated, err := scanDish(r.pool.QueryRow(
		ctx,
		query,
		establishmentID,
		dishID,
		patch.CategoryID,
		patch.Name,
		patch.Description,
		patch.PriceMinor,
		patch.IsAvailable,
	))
	if err != nil {
		return domain.Dish{}, menuError("update dish", err)
	}

	return updated, nil
}

type menuQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func listCategories(
	ctx context.Context,
	queryer menuQueryer,
	establishmentID int64,
) ([]domain.Category, error) {
	query := `
SELECT ` + categoryColumns + `
FROM menu_categories
WHERE establishment_id = $1
ORDER BY id`

	rows, err := queryer.Query(ctx, query, establishmentID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	categories := make([]domain.Category, 0)
	for rows.Next() {
		category, scanErr := scanCategory(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan category: %w", scanErr)
		}

		categories = append(categories, category)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate categories: %w", err)
	}

	return categories, nil
}

func listDishes(
	ctx context.Context,
	queryer menuQueryer,
	establishmentID int64,
) ([]domain.Dish, error) {
	query := `
SELECT
    d.external_id,
    d.id,
    d.category_id,
    d.name,
    d.description,
    d.price_minor,
    d.is_available,
    d.created_at,
    d.updated_at
FROM dishes AS d
JOIN menu_categories AS c ON c.id = d.category_id
WHERE c.establishment_id = $1
ORDER BY d.id`

	rows, err := queryer.Query(ctx, query, establishmentID)
	if err != nil {
		return nil, fmt.Errorf("list dishes: %w", err)
	}
	defer rows.Close()

	dishes := make([]domain.Dish, 0)
	for rows.Next() {
		dish, scanErr := scanDish(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan dish: %w", scanErr)
		}

		dishes = append(dishes, dish)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dishes: %w", err)
	}

	return dishes, nil
}

func scanCategory(row rowScanner) (domain.Category, error) {
	var category domain.Category

	err := row.Scan(
		&category.ID,
		&category.EstablishmentID,
		&category.Name,
		&category.CreatedAt,
		&category.UpdatedAt,
	)
	if err != nil {
		return domain.Category{}, err
	}

	return category, nil
}

func scanDish(row rowScanner) (domain.Dish, error) {
	var dish domain.Dish

	err := row.Scan(
		&dish.ExternalID,
		&dish.ID,
		&dish.CategoryID,
		&dish.Name,
		&dish.Description,
		&dish.PriceMinor,
		&dish.IsAvailable,
		&dish.CreatedAt,
		&dish.UpdatedAt,
	)
	if err != nil {
		return domain.Dish{}, err
	}

	return dish, nil
}

func menuError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, domain.ErrNotFound)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%s: %w", operation, domain.ErrConflict)
	}

	return fmt.Errorf("%s: %w", operation, err)
}
