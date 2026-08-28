package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kitchen-api/internal/kitchen/domain"
)

const establishmentColumns = `
    id,
    name,
    accepts_orders,
    api_key_hash,
    created_at,
    updated_at`

func (r *Repository) GetEstablishment(ctx context.Context, id int64) (domain.Establishment, error) {
	query := `SELECT ` + establishmentColumns + ` FROM establishments WHERE id = $1`

	establishment, err := scanEstablishment(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		return domain.Establishment{}, establishmentError("get establishment", err)
	}

	return establishment, nil
}

func (r *Repository) GetEstablishmentByAPIKeyHash(
	ctx context.Context,
	apiKeyHash []byte,
) (domain.Establishment, error) {
	query := `SELECT ` + establishmentColumns + ` FROM establishments WHERE api_key_hash = $1`

	establishment, err := scanEstablishment(r.pool.QueryRow(ctx, query, apiKeyHash))
	if err != nil {
		return domain.Establishment{}, establishmentError("get establishment by API key hash", err)
	}

	return establishment, nil
}

func (r *Repository) ListEstablishments(
	ctx context.Context,
	search string,
	limit int32,
	offset int64,
) ([]domain.Establishment, int64, error) {
	const filter = `
WHERE $1 = ''
   OR e.name ILIKE '%' || $1 || '%'
   OR EXISTS (
       SELECT 1
       FROM menu_categories AS c
       JOIN dishes AS d ON d.category_id = c.id
       WHERE c.establishment_id = e.id
         AND d.name ILIKE '%' || $1 || '%'
   )`

	countQuery := `SELECT count(*) FROM establishments AS e ` + filter

	var total int64
	if err := r.pool.QueryRow(ctx, countQuery, search).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count establishments: %w", err)
	}

	listQuery := `
SELECT
    e.id,
    e.name,
    e.accepts_orders,
    e.api_key_hash,
    e.created_at,
    e.updated_at
FROM establishments AS e ` + filter + `
ORDER BY e.id
LIMIT $2 OFFSET $3`

	rows, err := r.pool.Query(ctx, listQuery, search, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list establishments: %w", err)
	}
	defer rows.Close()

	establishments := make([]domain.Establishment, 0)
	for rows.Next() {
		establishment, scanErr := scanEstablishment(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan establishment: %w", scanErr)
		}

		establishments = append(establishments, establishment)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate establishments: %w", err)
	}

	return establishments, total, nil
}

func (r *Repository) UpdateEstablishmentAcceptsOrders(
	ctx context.Context,
	id int64,
	acceptsOrders bool,
) (domain.Establishment, error) {
	query := `
UPDATE establishments
SET accepts_orders = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING ` + establishmentColumns

	establishment, err := scanEstablishment(r.pool.QueryRow(ctx, query, id, acceptsOrders))
	if err != nil {
		return domain.Establishment{}, establishmentError("update establishment", err)
	}

	return establishment, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEstablishment(row rowScanner) (domain.Establishment, error) {
	var establishment domain.Establishment

	err := row.Scan(
		&establishment.ID,
		&establishment.Name,
		&establishment.AcceptsOrders,
		&establishment.APIKeyHash,
		&establishment.CreatedAt,
		&establishment.UpdatedAt,
	)
	if err != nil {
		return domain.Establishment{}, err
	}

	return establishment, nil
}

func establishmentError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, domain.ErrNotFound)
	}

	return fmt.Errorf("%s: %w", operation, err)
}
