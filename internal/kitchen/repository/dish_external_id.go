package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"kitchen-api/internal/kitchen/domain"
)

func (r *Repository) GetDish(ctx context.Context, establishmentID, dishID int64) (domain.Dish, error) {
	dish, err := scanDish(r.pool.QueryRow(ctx, `SELECT `+dishColumns+` FROM dishes WHERE establishment_id=$1 AND id=$2`, establishmentID, dishID))
	if err != nil {
		return domain.Dish{}, menuError("get dish", err)
	}
	return dish, nil
}
func (r *Repository) GetDishByExternalID(ctx context.Context, establishmentID int64, externalID string) (domain.Dish, error) {
	dish, err := scanDish(r.pool.QueryRow(ctx, `SELECT `+dishColumns+` FROM dishes WHERE establishment_id=$1 AND external_id=$2`, establishmentID, externalID))
	if err != nil {
		return domain.Dish{}, menuError("find external dish", err)
	}
	return dish, nil
}
func (r *Repository) BindDishExternalID(ctx context.Context, establishmentID, dishID int64, externalID string) (domain.Dish, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Dish{}, fmt.Errorf("begin dish binding: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	dish, err := scanDish(tx.QueryRow(ctx, `SELECT `+dishColumns+` FROM dishes WHERE establishment_id=$1 AND id=$2 FOR UPDATE`, establishmentID, dishID))
	if err != nil {
		return domain.Dish{}, menuError("lock dish binding", err)
	}
	if dish.ExternalID != nil {
		if *dish.ExternalID != externalID {
			return domain.Dish{}, domain.ErrExternalIDAlreadyBound
		}
	} else {
		dish, err = scanDish(tx.QueryRow(ctx, `UPDATE dishes SET external_id=$3, updated_at=NOW() WHERE establishment_id=$1 AND id=$2 RETURNING `+dishColumns, establishmentID, dishID, externalID))
		if err != nil {
			return domain.Dish{}, dishError("bind dish", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Dish{}, fmt.Errorf("commit dish binding: %w", err)
	}
	return dish, nil
}
func dishError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "dishes_establishment_external_id_key" {
		return fmt.Errorf("%s: %w", operation, domain.ErrExternalIDConflict)
	}
	return menuError(operation, err)
}
