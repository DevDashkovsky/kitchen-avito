package repository

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"kitchen-api/internal/kitchen/domain"
	"kitchen-api/internal/kitchen/service"
)

const orderColumns = `
    id,
    establishment_id,
    tracking_token_hash,
    request_fingerprint,
    status,
    customer_name,
    customer_phone,
    delivery_address,
    comment,
    total_minor,
    rejection_reason,
    rejection_comment,
    created_at,
    updated_at`

func getOrderDishes(ctx context.Context, db menuQueryer, establishmentID int64, dishIDs []int64) ([]domain.Dish, error) {
	if len(dishIDs) == 0 {
		return []domain.Dish{}, nil
	}

	query := `
SELECT
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
  AND d.id = ANY($2)
ORDER BY d.id`

	rows, err := db.Query(ctx, query, establishmentID, dishIDs)
	if err != nil {
		return nil, fmt.Errorf("get order dishes: %w", err)
	}
	defer rows.Close()

	dishes := make([]domain.Dish, 0, len(dishIDs))
	for rows.Next() {
		dish, scanErr := scanDish(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan order dish: %w", scanErr)
		}

		dishes = append(dishes, dish)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order dishes: %w", err)
	}

	return dishes, nil
}

func (r *Repository) WithinOrderCreation(ctx context.Context, fn func(service.OrderCreationTx) error) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin order creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(&orderCreationTx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit order creation: %w", err)
	}
	return nil
}

type orderCreationTx struct{ tx pgx.Tx }

func (t *orderCreationTx) LockKey(ctx context.Context, hash []byte) error {
	if len(hash) != 32 {
		return domain.ErrInvalidInput
	}
	// Collisions only serialize unrelated requests; lookup uses the full hash.
	key := int64(binary.BigEndian.Uint64(hash[:8]))
	if _, err := t.tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", key); err != nil {
		return fmt.Errorf("lock order key: %w", err)
	}
	return nil
}

func (t *orderCreationTx) FindByTokenHash(ctx context.Context, hash []byte) (domain.Order, error) {
	order, err := scanOrder(t.tx.QueryRow(ctx, "SELECT "+orderColumns+" FROM orders WHERE tracking_token_hash = $1", hash))
	if err != nil {
		return domain.Order{}, orderNotFoundError("find order replay", err)
	}
	return order, nil
}

func (t *orderCreationTx) GetEstablishment(ctx context.Context, id int64) (domain.Establishment, error) {
	establishment, err := scanEstablishment(t.tx.QueryRow(ctx, "SELECT "+establishmentColumns+" FROM establishments WHERE id = $1", id))
	if err != nil {
		return domain.Establishment{}, establishmentError("get creation establishment", err)
	}
	return establishment, nil
}

func (t *orderCreationTx) GetOrderDishes(ctx context.Context, establishmentID int64, ids []int64) ([]domain.Dish, error) {
	return getOrderDishes(ctx, t.tx, establishmentID, ids)
}

func (t *orderCreationTx) CreateOrder(ctx context.Context, order domain.Order) (domain.Order, error) {
	tx := t.tx
	if len(order.Items) == 0 {
		return domain.Order{}, domain.ErrInvalidInput
	}
	var acceptsOrders bool
	err := tx.QueryRow(ctx, `
        SELECT accepts_orders
        FROM establishments
        WHERE id = $1
        FOR SHARE`, order.EstablishmentID).Scan(&acceptsOrders)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, fmt.Errorf("lock order establishment: %w", domain.ErrNotFound)
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("lock order establishment: %w", err)
	}
	if !acceptsOrders {
		return domain.Order{}, fmt.Errorf("create order: establishment does not accept orders: %w", domain.ErrConflict)
	}

	if err := lockAndCheckOrderDishes(ctx, tx, order); err != nil {
		return domain.Order{}, err
	}

	query := `
INSERT INTO orders (
    establishment_id,
    tracking_token_hash,
    request_fingerprint,
    status,
    customer_name,
    customer_phone,
    delivery_address,
    comment,
    total_minor
)
VALUES ($1, $2, $8, 'pending_confirmation', $3, $4, $5, $6, $7)
RETURNING ` + orderColumns

	created, err := scanOrder(tx.QueryRow(
		ctx,
		query,
		order.EstablishmentID,
		order.TrackingTokenHash,
		order.CustomerName,
		order.CustomerPhone,
		order.DeliveryAddress,
		order.Comment,
		order.TotalMinor,
		order.RequestFingerprint,
	))
	if err != nil {
		return domain.Order{}, fmt.Errorf("insert order: %w", err)
	}

	created.Items = make([]domain.OrderItem, len(order.Items))
	for index, item := range order.Items {
		var itemID int64
		err := tx.QueryRow(ctx, `
            INSERT INTO order_items (
                order_id,
                dish_id,
                dish_name,
                unit_price_minor,
                quantity
            )
            VALUES ($1, $2, $3, $4, $5)
            RETURNING id`,
			created.ID,
			item.DishID,
			item.DishName,
			item.UnitPriceMinor,
			item.Quantity,
		).Scan(&itemID)
		if err != nil {
			return domain.Order{}, fmt.Errorf("insert order item: %w", err)
		}

		item.ID = itemID
		item.OrderID = created.ID
		created.Items[index] = item
	}

	return created, nil
}

func (r *Repository) GetOrderByTrackingTokenHash(
	ctx context.Context,
	trackingTokenHash []byte,
) (domain.Order, error) {
	query := `SELECT ` + orderColumns + ` FROM orders WHERE tracking_token_hash = $1`

	order, err := scanOrder(r.pool.QueryRow(ctx, query, trackingTokenHash))
	if err != nil {
		return domain.Order{}, orderNotFoundError("get order by tracking token hash", err)
	}

	items, err := r.listOrderItems(ctx, []int64{order.ID})
	if err != nil {
		return domain.Order{}, err
	}
	order.Items = items[order.ID]

	return order, nil
}

func (r *Repository) GetOrder(
	ctx context.Context,
	establishmentID int64,
	orderID int64,
) (domain.Order, error) {
	query := `
SELECT ` + orderColumns + `
FROM orders
WHERE id = $2
  AND establishment_id = $1`

	order, err := scanOrder(r.pool.QueryRow(ctx, query, establishmentID, orderID))
	if err != nil {
		return domain.Order{}, orderNotFoundError("get order", err)
	}

	items, err := r.listOrderItems(ctx, []int64{order.ID})
	if err != nil {
		return domain.Order{}, err
	}
	order.Items = items[order.ID]

	return order, nil
}

func (r *Repository) ListOrders(
	ctx context.Context,
	establishmentID int64,
	status domain.OrderStatus,
	limit int32,
	cursorCreatedAt *time.Time,
	cursorID *int64,
) ([]domain.Order, int64, error) {
	var total int64
	err := r.pool.QueryRow(ctx, `
        SELECT count(*)
        FROM orders
        WHERE establishment_id = $1
          AND status = $2`, establishmentID, status).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}

	query := `
SELECT ` + orderColumns + `
FROM orders
WHERE establishment_id = $1
  AND status = $2
  AND ($3::TIMESTAMPTZ IS NULL OR (created_at, id) > ($3, $4))
ORDER BY created_at, id
LIMIT $5`

	rows, err := r.pool.Query(
		ctx,
		query,
		establishmentID,
		status,
		cursorCreatedAt,
		cursorID,
		limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	orders := make([]domain.Order, 0)
	orderIDs := make([]int64, 0)
	for rows.Next() {
		order, scanErr := scanOrder(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan order: %w", scanErr)
		}

		orders = append(orders, order)
		orderIDs = append(orderIDs, order.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate orders: %w", err)
	}

	items, err := r.listOrderItems(ctx, orderIDs)
	if err != nil {
		return nil, 0, err
	}
	for index := range orders {
		orders[index].Items = items[orders[index].ID]
	}

	return orders, total, nil
}

func (r *Repository) UpdateOrderStatus(
	ctx context.Context,
	establishmentID int64,
	orderID int64,
	expectedStatus domain.OrderStatus,
	nextStatus domain.OrderStatus,
	rejectionReason *domain.RejectionReason,
	rejectionComment *string,
	confirmationTimeout time.Duration,
) (domain.Order, error) {
	query := `
UPDATE orders
SET status = CASE
        WHEN status = 'pending_confirmation'
         AND created_at <= NOW() - $7::interval
        THEN 'rejected'
        ELSE $4
    END,
    rejection_reason = CASE
        WHEN status = 'pending_confirmation'
         AND created_at <= NOW() - $7::interval
        THEN 'confirmation_timeout'
        ELSE $5
    END,
    rejection_comment = CASE
        WHEN status = 'pending_confirmation'
         AND created_at <= NOW() - $7::interval
        THEN NULL
        ELSE $6
    END,
    updated_at = NOW()
WHERE id = $2
  AND establishment_id = $1
  AND status = $3
RETURNING ` + orderColumns

	order, err := scanOrder(r.pool.QueryRow(
		ctx,
		query,
		establishmentID,
		orderID,
		expectedStatus,
		nextStatus,
		rejectionReasonValue(rejectionReason),
		rejectionComment,
		confirmationTimeout.String(),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, fmt.Errorf("update order status: %w", domain.ErrConflict)
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("update order status: %w", err)
	}

	return order, nil
}

func (r *Repository) CancelOrder(
	ctx context.Context,
	trackingTokenHash []byte,
	confirmationTimeout time.Duration,
) (domain.Order, error) {
	query := `
UPDATE orders
SET status = CASE
        WHEN created_at <= NOW() - $2::interval THEN 'rejected'
        ELSE 'cancelled'
    END,
    rejection_reason = CASE
        WHEN created_at <= NOW() - $2::interval THEN 'confirmation_timeout'
        ELSE NULL
    END,
    rejection_comment = NULL,
    updated_at = NOW()
WHERE tracking_token_hash = $1
  AND status = 'pending_confirmation'
RETURNING ` + orderColumns

	order, err := scanOrder(r.pool.QueryRow(ctx, query, trackingTokenHash, confirmationTimeout.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Order{}, fmt.Errorf("cancel order: %w", domain.ErrConflict)
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("cancel order: %w", err)
	}

	return order, nil
}

func (r *Repository) RejectExpiredOrders(ctx context.Context, confirmationTimeout time.Duration) (int64, error) {
	commandTag, err := r.pool.Exec(ctx, `
        UPDATE orders
        SET status = 'rejected',
            rejection_reason = 'confirmation_timeout',
            rejection_comment = NULL,
            updated_at = NOW()
        WHERE status = 'pending_confirmation'
          AND created_at <= NOW() - $1::interval`, confirmationTimeout.String())
	if err != nil {
		return 0, fmt.Errorf("reject expired orders: %w", err)
	}

	return commandTag.RowsAffected(), nil
}

func lockAndCheckOrderDishes(ctx context.Context, tx pgx.Tx, order domain.Order) error {
	dishIDs := make([]int64, len(order.Items))
	requestedIndexes := make(map[int64]int, len(order.Items))
	for index, item := range order.Items {
		if item.Quantity <= 0 {
			return fmt.Errorf("check order item: %w", domain.ErrInvalidInput)
		}

		dishIDs[index] = item.DishID
		requestedIndexes[item.DishID] = index
	}

	rows, err := tx.Query(ctx, `
        SELECT d.id, d.name, d.price_minor, d.is_available
        FROM dishes AS d
        JOIN menu_categories AS c ON c.id = d.category_id
        WHERE c.establishment_id = $1
          AND d.id = ANY($2)
        ORDER BY d.id
        FOR SHARE OF d`, order.EstablishmentID, dishIDs)
	if err != nil {
		return fmt.Errorf("lock order dishes: %w", err)
	}
	defer rows.Close()

	currentDishes := make([]domain.Dish, 0, len(order.Items))
	menuChanged := false
	for rows.Next() {
		var (
			dishID      int64
			dishName    string
			priceMinor  int64
			isAvailable bool
		)

		if err := rows.Scan(&dishID, &dishName, &priceMinor, &isAvailable); err != nil {
			return fmt.Errorf("scan locked order dish: %w", err)
		}

		index, ok := requestedIndexes[dishID]
		if !ok {
			menuChanged = true
			continue
		}

		currentDishes = append(currentDishes, domain.Dish{
			ID:          dishID,
			Name:        dishName,
			PriceMinor:  priceMinor,
			IsAvailable: isAvailable,
		})
		item := &order.Items[index]
		item.DishName = dishName
		if !isAvailable || item.UnitPriceMinor != priceMinor {
			menuChanged = true
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate locked order dishes: %w", err)
	}
	if len(currentDishes) != len(order.Items) {
		menuChanged = true
	}
	if menuChanged {
		return &domain.MenuChangedError{Dishes: currentDishes}
	}

	return nil
}

func (r *Repository) listOrderItems(
	ctx context.Context,
	orderIDs []int64,
) (map[int64][]domain.OrderItem, error) {
	itemsByOrder := make(map[int64][]domain.OrderItem, len(orderIDs))
	if len(orderIDs) == 0 {
		return itemsByOrder, nil
	}

	rows, err := r.pool.Query(ctx, `
        SELECT id, order_id, dish_id, dish_name, unit_price_minor, quantity
        FROM order_items
        WHERE order_id = ANY($1)
        ORDER BY order_id, id`, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("list order items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item domain.OrderItem
		err := rows.Scan(
			&item.ID,
			&item.OrderID,
			&item.DishID,
			&item.DishName,
			&item.UnitPriceMinor,
			&item.Quantity,
		)
		if err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}

		itemsByOrder[item.OrderID] = append(itemsByOrder[item.OrderID], item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order items: %w", err)
	}

	for _, orderID := range orderIDs {
		if itemsByOrder[orderID] == nil {
			itemsByOrder[orderID] = []domain.OrderItem{}
		}
	}

	return itemsByOrder, nil
}

func scanOrder(row rowScanner) (domain.Order, error) {
	var (
		order            domain.Order
		status           string
		comment          pgtype.Text
		rejectionReason  pgtype.Text
		rejectionComment pgtype.Text
	)

	err := row.Scan(
		&order.ID,
		&order.EstablishmentID,
		&order.TrackingTokenHash,
		&order.RequestFingerprint,
		&status,
		&order.CustomerName,
		&order.CustomerPhone,
		&order.DeliveryAddress,
		&comment,
		&order.TotalMinor,
		&rejectionReason,
		&rejectionComment,
		&order.CreatedAt,
		&order.UpdatedAt,
	)
	if err != nil {
		return domain.Order{}, err
	}

	order.Status = domain.OrderStatus(status)
	if comment.Valid {
		order.Comment = &comment.String
	}
	if rejectionReason.Valid {
		reason := domain.RejectionReason(rejectionReason.String)
		order.RejectionReason = &reason
	}
	if rejectionComment.Valid {
		order.RejectionComment = &rejectionComment.String
	}

	return order, nil
}

func orderNotFoundError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, domain.ErrNotFound)
	}

	return fmt.Errorf("%s: %w", operation, err)
}

func rejectionReasonValue(reason *domain.RejectionReason) any {
	if reason == nil {
		return nil
	}

	return string(*reason)
}
