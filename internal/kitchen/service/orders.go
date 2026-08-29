package service

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	GetOrderDishes(context.Context, int64, []int64) ([]domain.Dish, error)
	CreateOrder(context.Context, domain.Order) (domain.Order, error)
	GetOrderByTrackingTokenHash(context.Context, []byte) (domain.Order, error)
	GetOrder(context.Context, int64, int64) (domain.Order, error)
	ListOrders(context.Context, int64, domain.OrderStatus, int32) ([]domain.Order, int64, error)
	UpdateOrderStatus(
		context.Context,
		int64,
		int64,
		domain.OrderStatus,
		domain.OrderStatus,
		*domain.RejectionReason,
		*string,
	) (domain.Order, error)
	CancelOrder(context.Context, []byte) (domain.Order, error)
	RejectExpiredOrders(context.Context, time.Time) (int64, error)
}

type OrderService struct {
	repository orderRepository
}

func NewOrderService(repository orderRepository) *OrderService {
	return &OrderService{repository: repository}
}

type CreateOrderItem struct {
	DishID                 int64
	Quantity               int32
	ExpectedUnitPriceMinor int64
}

type CreateOrderInput struct {
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

func (s *OrderService) Create(
	ctx context.Context,
	input CreateOrderInput,
) (domain.Order, error) {
	normalized, err := normalizeCreateOrderInput(input)
	if err != nil {
		return domain.Order{}, err
	}

	establishment, err := s.repository.GetEstablishment(ctx, normalized.EstablishmentID)
	if err != nil {
		return domain.Order{}, fmt.Errorf("get order establishment: %w", err)
	}
	if !establishment.AcceptsOrders {
		return domain.Order{}, &OrderConflictError{Conflicts: []DishConflict{}}
	}

	items, totalMinor, conflicts, err := s.prepareOrderItems(ctx, normalized)
	if err != nil {
		return domain.Order{}, err
	}
	if len(conflicts) > 0 {
		return domain.Order{}, &OrderConflictError{Conflicts: conflicts}
	}

	order, err := s.repository.CreateOrder(ctx, domain.Order{
		EstablishmentID:   normalized.EstablishmentID,
		TrackingTokenHash: normalized.TrackingTokenHash,
		Status:            domain.OrderStatusPendingConfirmation,
		CustomerName:      normalized.CustomerName,
		CustomerPhone:     normalized.CustomerPhone,
		DeliveryAddress:   normalized.DeliveryAddress,
		Comment:           normalized.Comment,
		TotalMinor:        totalMinor,
		Items:             items,
	})
	if errors.Is(err, domain.ErrConflict) {
		return domain.Order{}, &OrderConflictError{Conflicts: []DishConflict{}}
	}
	if err != nil {
		return domain.Order{}, fmt.Errorf("create order: %w", err)
	}

	return order, nil
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

	cancelled, err := s.repository.CancelOrder(ctx, trackingTokenHash)
	if !errors.Is(err, domain.ErrConflict) {
		if err != nil {
			return domain.Order{}, fmt.Errorf("cancel order: %w", err)
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
) ([]domain.Order, int64, error) {
	if !status.IsValid() || limit < 1 || limit > 100 {
		return nil, 0, domain.ErrInvalidInput
	}

	orders, total, err := s.repository.ListOrders(ctx, establishmentID, status, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list partner orders: %w", err)
	}

	return orders, total, nil
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
	)
	if !errors.Is(err, domain.ErrConflict) {
		if err != nil {
			return domain.Order{}, fmt.Errorf("update partner order status: %w", err)
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

func (s *OrderService) RejectExpired(ctx context.Context, createdBefore time.Time) (int64, error) {
	rejected, err := s.repository.RejectExpiredOrders(ctx, createdBefore)
	if err != nil {
		return 0, fmt.Errorf("reject expired orders: %w", err)
	}

	return rejected, nil
}

func (s *OrderService) prepareOrderItems(
	ctx context.Context,
	input CreateOrderInput,
) ([]domain.OrderItem, int64, []DishConflict, error) {
	combined, dishIDs, err := combineOrderItems(input.Items)
	if err != nil {
		return nil, 0, nil, err
	}

	dishes, err := s.repository.GetOrderDishes(ctx, input.EstablishmentID, dishIDs)
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

	if input.EstablishmentID < 1 || len(input.TrackingTokenHash) == 0 ||
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
