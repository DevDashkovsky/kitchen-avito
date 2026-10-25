package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"kitchen-api/internal/kitchen/domain"
)

const (
	maxOrderItems        = 50
	maxOrderItemQuantity = int32(100)
)

type orderRepository interface {
	GetEstablishment(context.Context, int64) (domain.Establishment, error)
	WithinOrderCreation(context.Context, func(OrderCreationTx) error) error
	GetOrderByTrackingTokenHash(context.Context, []byte) (domain.Order, error)
	GetOrder(context.Context, int64, int64) (domain.Order, error)
	ListOrders(
		context.Context,
		int64,
		domain.OrderStatus,
		int32,
		*time.Time,
		*int64,
	) ([]domain.Order, int64, error)
	UpdateOrderStatus(
		context.Context,
		int64,
		int64,
		domain.OrderStatus,
		domain.OrderStatus,
		*domain.RejectionReason,
		*string,
		time.Duration,
	) (domain.Order, error)
	CancelOrder(context.Context, []byte, time.Duration) (domain.Order, error)
	RejectExpiredOrders(context.Context, time.Duration) (int64, error)
}

// OrderCreationTx scopes creation and replay to one database transaction.
type OrderCreationTx interface {
	GetEstablishment(context.Context, int64) (domain.Establishment, error)
	GetOrderDishes(context.Context, int64, []int64) ([]domain.Dish, error)
	LockKey(context.Context, []byte) error
	FindByTokenHash(context.Context, []byte) (domain.Order, error)
	CreateOrder(context.Context, domain.Order) (domain.Order, error)
}

type OrderService struct {
	repository          orderRepository
	confirmationTimeout time.Duration
}

func NewOrderService(repository orderRepository, confirmationTimeout time.Duration) *OrderService {
	return &OrderService{repository: repository, confirmationTimeout: confirmationTimeout}
}

type CreateOrderItem struct {
	DishID                 int64 `json:"dish_id"`
	Quantity               int32 `json:"quantity"`
	ExpectedUnitPriceMinor int64 `json:"expected_unit_price_minor"`
}

type CreateOrderInput struct {
	Idempotent        bool
	EstablishmentID   int64
	TrackingTokenHash []byte
	CustomerName      string
	CustomerPhone     string
	DeliveryAddress   string
	Comment           *string
	Items             []CreateOrderItem
}

type DishConflictReason string

const (
	DishConflictPriceChanged    DishConflictReason = "price_changed"
	DishConflictDishUnavailable DishConflictReason = "dish_unavailable"
)

type DishConflict struct {
	DishID                int64
	Reason                DishConflictReason
	CurrentUnitPriceMinor *int64
}

type OrderConflictError struct {
	Conflicts []DishConflict
}

func (e *OrderConflictError) Error() string {
	return "order conflicts with current menu state"
}

func (e *OrderConflictError) Unwrap() error {
	return domain.ErrConflict
}

func (s *OrderService) Create(ctx context.Context, input CreateOrderInput) (domain.Order, bool, error) {
	normalized, err := normalizeCreateOrderInput(input)
	if err != nil {
		return domain.Order{}, false, err
	}
	normalized.Items, _, err = combineOrderItems(normalized.Items)
	if err != nil {
		return domain.Order{}, false, err
	}
	var fingerprint []byte
	if normalized.Idempotent {
		fingerprint, err = orderFingerprint(normalized)
		if err != nil {
			return domain.Order{}, false, err
		}
	}
	var order domain.Order
	var replay bool
	err = s.repository.WithinOrderCreation(ctx, func(tx OrderCreationTx) error {
		if normalized.Idempotent {
			if err := tx.LockKey(ctx, normalized.TrackingTokenHash); err != nil {
				return err
			}
			existing, err := tx.FindByTokenHash(ctx, normalized.TrackingTokenHash)
			switch {
			case err == nil:
				if !bytes.Equal(existing.RequestFingerprint, fingerprint) {
					return domain.ErrIdempotencyKeyReused
				}
				order, replay = existing, true
				return nil
			case !errors.Is(err, domain.ErrNotFound):
				return err
			}
		}
		establishment, err := tx.GetEstablishment(ctx, normalized.EstablishmentID)
		if err != nil {
			return fmt.Errorf("get order establishment: %w", err)
		}
		if !establishment.AcceptsOrders {
			return &OrderConflictError{Conflicts: []DishConflict{}}
		}
		items, totalMinor, conflicts, err := s.prepareOrderItems(ctx, tx, normalized)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			return &OrderConflictError{Conflicts: conflicts}
		}
		order, err = tx.CreateOrder(ctx, domain.Order{
			EstablishmentID: normalized.EstablishmentID, TrackingTokenHash: normalized.TrackingTokenHash,
			RequestFingerprint: fingerprint, Status: domain.OrderStatusPendingConfirmation,
			CustomerName: normalized.CustomerName, CustomerPhone: normalized.CustomerPhone,
			DeliveryAddress: normalized.DeliveryAddress, Comment: normalized.Comment,
			TotalMinor: totalMinor, Items: items,
		})
		var menuChanged *domain.MenuChangedError
		if errors.As(err, &menuChanged) {
			return &OrderConflictError{Conflicts: dishConflictsFromCurrentState(normalized.Items, menuChanged.Dishes)}
		}
		if errors.Is(err, domain.ErrConflict) {
			return &OrderConflictError{Conflicts: []DishConflict{}}
		}
		return err
	})
	if err != nil {
		return domain.Order{}, false, fmt.Errorf("create order: %w", err)
	}
	return order, replay, nil
}

// orderFingerprint is versioned independently of transport JSON. Callers pass
// normalized input with duplicate dish lines combined by the existing rules.
func orderFingerprint(input CreateOrderInput) ([]byte, error) {
	items := append([]CreateOrderItem(nil), input.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].DishID < items[j].DishID })
	canonical := struct {
		Version         int               `json:"version"`
		EstablishmentID int64             `json:"establishment_id"`
		Name            string            `json:"name"`
		Phone           string            `json:"phone"`
		Address         string            `json:"address"`
		Comment         *string           `json:"comment"`
		Items           []CreateOrderItem `json:"items"`
	}{
		Version: 1, EstablishmentID: input.EstablishmentID,
		Name: input.CustomerName, Phone: input.CustomerPhone, Address: input.DeliveryAddress,
		Comment: input.Comment, Items: items,
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode order fingerprint: %w", err)
	}
	hash := sha256.Sum256(payload)
	return hash[:], nil
}

func (s *OrderService) GetTracked(
	ctx context.Context,
	trackingTokenHash []byte,
) (domain.Order, domain.Establishment, error) {
	if len(trackingTokenHash) == 0 {
		return domain.Order{}, domain.Establishment{}, domain.ErrUnauthorized
	}

	order, err := s.repository.GetOrderByTrackingTokenHash(ctx, trackingTokenHash)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Order{}, domain.Establishment{}, domain.ErrUnauthorized
	}
	if err != nil {
		return domain.Order{}, domain.Establishment{}, fmt.Errorf("get tracked order: %w", err)
	}

	establishment, err := s.repository.GetEstablishment(ctx, order.EstablishmentID)
	if err != nil {
		return domain.Order{}, domain.Establishment{}, fmt.Errorf("get tracked order establishment: %w", err)
	}

	return order, establishment, nil
}

func (s *OrderService) Cancel(
	ctx context.Context,
	trackingTokenHash []byte,
) (domain.Order, error) {
	order, _, err := s.GetTracked(ctx, trackingTokenHash)
	if err != nil {
		return domain.Order{}, err
	}
	if order.Status == domain.OrderStatusCancelled {
		return order, nil
	}
	if order.Status != domain.OrderStatusPendingConfirmation {
		return domain.Order{}, domain.ErrCancellationNotAllowed
	}

	cancelled, err := s.repository.CancelOrder(ctx, trackingTokenHash, s.confirmationTimeout)
	if !errors.Is(err, domain.ErrConflict) {
		if err != nil {
			return domain.Order{}, fmt.Errorf("cancel order: %w", err)
		}
		if cancelled.Status != domain.OrderStatusCancelled {
			return domain.Order{}, domain.ErrCancellationNotAllowed
		}
		return cancelled, nil
	}

	current, _, getErr := s.GetTracked(ctx, trackingTokenHash)
	if getErr != nil {
		return domain.Order{}, getErr
	}
	if current.Status == domain.OrderStatusCancelled {
		return current, nil
	}

	return domain.Order{}, domain.ErrCancellationNotAllowed
}

func (s *OrderService) ListPartnerOrders(
	ctx context.Context,
	establishmentID int64,
	status domain.OrderStatus,
	limit int32,
	cursor *string,
) ([]domain.Order, int64, *string, error) {
	if !status.IsValid() || limit < 1 || limit > 100 {
		return nil, 0, nil, domain.ErrInvalidInput
	}

	position, err := decodeOrderCursor(cursor)
	if err != nil {
		return nil, 0, nil, domain.ErrInvalidInput
	}

	queryLimit := limit + 1
	orders, total, err := s.repository.ListOrders(
		ctx,
		establishmentID,
		status,
		queryLimit,
		position.createdAt,
		position.id,
	)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("list partner orders: %w", err)
	}

	var nextCursor *string
	if len(orders) > int(limit) {
		orders = orders[:limit]
		encoded, encodeErr := encodeOrderCursor(orders[len(orders)-1])
		if encodeErr != nil {
			return nil, 0, nil, fmt.Errorf("encode next order cursor: %w", encodeErr)
		}
		nextCursor = &encoded
	}

	return orders, total, nextCursor, nil
}

type orderCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id"`
}

type decodedOrderCursor struct {
	createdAt *time.Time
	id        *int64
}

func decodeOrderCursor(cursor *string) (decodedOrderCursor, error) {
	if cursor == nil {
		return decodedOrderCursor{}, nil
	}

	payload, err := base64.RawURLEncoding.DecodeString(*cursor)
	if err != nil {
		return decodedOrderCursor{}, fmt.Errorf("decode cursor: %w", err)
	}

	var decoded orderCursor
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return decodedOrderCursor{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	if decoded.ID < 1 || decoded.CreatedAt.IsZero() {
		return decodedOrderCursor{}, fmt.Errorf("invalid cursor values")
	}

	return decodedOrderCursor{createdAt: &decoded.CreatedAt, id: &decoded.ID}, nil
}

func encodeOrderCursor(order domain.Order) (string, error) {
	payload, err := json.Marshal(orderCursor{CreatedAt: order.CreatedAt, ID: order.ID})
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func (s *OrderService) GetPartnerOrder(
	ctx context.Context,
	establishmentID int64,
	orderID int64,
) (domain.Order, error) {
	order, err := s.repository.GetOrder(ctx, establishmentID, orderID)
	if err != nil {
		return domain.Order{}, fmt.Errorf("get partner order: %w", err)
	}

	return order, nil
}

func (s *OrderService) UpdatePartnerStatus(
	ctx context.Context,
	establishmentID int64,
	orderID int64,
	nextStatus domain.OrderStatus,
	rejectionReason *domain.RejectionReason,
	rejectionComment *string,
) (domain.Order, error) {
	if err := validatePartnerStatusUpdate(nextStatus, rejectionReason, rejectionComment); err != nil {
		return domain.Order{}, err
	}

	current, err := s.repository.GetOrder(ctx, establishmentID, orderID)
	if err != nil {
		return domain.Order{}, fmt.Errorf("get order for status update: %w", err)
	}
	if current.Status == nextStatus {
		return current, nil
	}
	if !current.Status.CanTransitionTo(nextStatus) {
		return domain.Order{}, domain.ErrInvalidStatusTransition
	}

	updated, err := s.repository.UpdateOrderStatus(
		ctx,
		establishmentID,
		orderID,
		current.Status,
		nextStatus,
		rejectionReason,
		rejectionComment,
		s.confirmationTimeout,
	)
	if !errors.Is(err, domain.ErrConflict) {
		if err != nil {
			return domain.Order{}, fmt.Errorf("update partner order status: %w", err)
		}
		if updated.RejectionReason != nil &&
			*updated.RejectionReason == domain.RejectionReasonConfirmationTimeout {
			return domain.Order{}, domain.ErrInvalidStatusTransition
		}
		if updated.Status != nextStatus {
			return domain.Order{}, domain.ErrInvalidStatusTransition
		}
		return updated, nil
	}

	latest, getErr := s.repository.GetOrder(ctx, establishmentID, orderID)
	if getErr != nil {
		return domain.Order{}, fmt.Errorf("reload order after status conflict: %w", getErr)
	}
	if latest.Status == nextStatus {
		return latest, nil
	}

	return domain.Order{}, domain.ErrInvalidStatusTransition
}

func (s *OrderService) RejectExpired(ctx context.Context, confirmationTimeout time.Duration) (int64, error) {
	rejected, err := s.repository.RejectExpiredOrders(ctx, confirmationTimeout)
	if err != nil {
		return 0, fmt.Errorf("reject expired orders: %w", err)
	}

	return rejected, nil
}

func dishConflictsFromCurrentState(
	requested []CreateOrderItem,
	dishes []domain.Dish,
) []DishConflict {
	dishesByID := make(map[int64]domain.Dish, len(dishes))
	for _, dish := range dishes {
		dishesByID[dish.ID] = dish
	}

	conflicts := make([]DishConflict, 0)
	processed := make(map[int64]struct{}, len(requested))
	for _, item := range requested {
		if _, exists := processed[item.DishID]; exists {
			continue
		}
		processed[item.DishID] = struct{}{}

		dish, found := dishesByID[item.DishID]
		if !found || !dish.IsAvailable {
			conflicts = append(conflicts, DishConflict{
				DishID: item.DishID,
				Reason: DishConflictDishUnavailable,
			})
			continue
		}
		if dish.PriceMinor != item.ExpectedUnitPriceMinor {
			currentPrice := dish.PriceMinor
			conflicts = append(conflicts, DishConflict{
				DishID:                item.DishID,
				Reason:                DishConflictPriceChanged,
				CurrentUnitPriceMinor: &currentPrice,
			})
		}
	}

	return conflicts
}

func (s *OrderService) prepareOrderItems(
	ctx context.Context,
	tx OrderCreationTx,
	input CreateOrderInput,
) ([]domain.OrderItem, int64, []DishConflict, error) {
	combined, dishIDs, err := combineOrderItems(input.Items)
	if err != nil {
		return nil, 0, nil, err
	}

	dishes, err := tx.GetOrderDishes(ctx, input.EstablishmentID, dishIDs)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("get dishes for order: %w", err)
	}

	dishesByID := make(map[int64]domain.Dish, len(dishes))
	for _, dish := range dishes {
		dishesByID[dish.ID] = dish
	}

	items := make([]domain.OrderItem, 0, len(combined))
	conflicts := make([]DishConflict, 0)
	var totalMinor int64

	for _, requested := range combined {
		dish, found := dishesByID[requested.DishID]
		if !found || !dish.IsAvailable {
			conflicts = append(conflicts, DishConflict{
				DishID: requested.DishID,
				Reason: DishConflictDishUnavailable,
			})
			continue
		}
		if dish.PriceMinor != requested.ExpectedUnitPriceMinor {
			currentPrice := dish.PriceMinor
			conflicts = append(conflicts, DishConflict{
				DishID:                requested.DishID,
				Reason:                DishConflictPriceChanged,
				CurrentUnitPriceMinor: &currentPrice,
			})
			continue
		}

		quantity := int64(requested.Quantity)
		if dish.PriceMinor > math.MaxInt64/quantity || totalMinor > math.MaxInt64-dish.PriceMinor*quantity {
			return nil, 0, nil, domain.ErrInvalidInput
		}
		totalMinor += dish.PriceMinor * quantity

		items = append(items, domain.OrderItem{
			DishID:         dish.ID,
			DishName:       dish.Name,
			UnitPriceMinor: dish.PriceMinor,
			Quantity:       requested.Quantity,
		})
	}

	return items, totalMinor, conflicts, nil
}

func normalizeCreateOrderInput(input CreateOrderInput) (CreateOrderInput, error) {
	input.CustomerName = strings.TrimSpace(input.CustomerName)
	input.CustomerPhone = strings.TrimSpace(input.CustomerPhone)
	input.DeliveryAddress = strings.TrimSpace(input.DeliveryAddress)

	if input.EstablishmentID < 1 || len(input.TrackingTokenHash) != sha256.Size ||
		len(input.Items) == 0 || len(input.Items) > maxOrderItems ||
		len([]rune(input.CustomerName)) < 1 || len([]rune(input.CustomerName)) > 200 ||
		len([]rune(input.CustomerPhone)) < 5 || len([]rune(input.CustomerPhone)) > 32 ||
		len([]rune(input.DeliveryAddress)) < 1 || len([]rune(input.DeliveryAddress)) > 500 ||
		(input.Comment != nil && len([]rune(*input.Comment)) > 2000) {
		return CreateOrderInput{}, domain.ErrInvalidInput
	}

	return input, nil
}

func combineOrderItems(items []CreateOrderItem) ([]CreateOrderItem, []int64, error) {
	combined := make([]CreateOrderItem, 0, len(items))
	indexes := make(map[int64]int, len(items))
	dishIDs := make([]int64, 0, len(items))

	for _, item := range items {
		if item.DishID < 1 || item.Quantity < 1 || item.Quantity > maxOrderItemQuantity ||
			item.ExpectedUnitPriceMinor < 0 {
			return nil, nil, domain.ErrInvalidInput
		}

		index, exists := indexes[item.DishID]
		if !exists {
			indexes[item.DishID] = len(combined)
			combined = append(combined, item)
			dishIDs = append(dishIDs, item.DishID)
			continue
		}

		if combined[index].ExpectedUnitPriceMinor != item.ExpectedUnitPriceMinor ||
			item.Quantity > maxOrderItemQuantity-combined[index].Quantity {
			return nil, nil, domain.ErrInvalidInput
		}
		combined[index].Quantity += item.Quantity
	}

	return combined, dishIDs, nil
}

func validatePartnerStatusUpdate(
	status domain.OrderStatus,
	reason *domain.RejectionReason,
	comment *string,
) error {
	if status == domain.OrderStatusPendingConfirmation || status == domain.OrderStatusCancelled ||
		!status.IsValid() {
		return domain.ErrInvalidInput
	}

	if status != domain.OrderStatusRejected {
		if reason != nil || comment != nil {
			return domain.ErrInvalidInput
		}
		return nil
	}

	if reason == nil || !reason.IsValid() || *reason == domain.RejectionReasonConfirmationTimeout {
		return domain.ErrInvalidInput
	}
	if comment != nil && len([]rune(*comment)) > 2000 {
		return domain.ErrInvalidInput
	}
	if *reason == domain.RejectionReasonOther &&
		(comment == nil || strings.TrimSpace(*comment) == "") {
		return domain.ErrInvalidInput
	}

	return nil
}
