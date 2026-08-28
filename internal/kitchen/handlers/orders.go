package handlers

import (
	"context"
	"errors"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
	"kitchen-api/internal/kitchen/service"
)

func (h *Handler) CreateOrder(
	ctx context.Context,
	request api.CreateOrderRequestObject,
) (api.CreateOrderResponseObject, error) {
	if request.Body == nil {
		return api.CreateOrder400JSONResponse{
			BadRequestJSONResponse: badRequest("Тело запроса обязательно"),
		}, nil
	}

	trackingToken, trackingTokenHash, err := generateTrackingToken()
	if err != nil {
		h.logError("generate tracking token", err)
		return api.CreateOrder500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	items := make([]service.CreateOrderItem, len(request.Body.OrderItems))
	for index, item := range request.Body.OrderItems {
		items[index] = service.CreateOrderItem{
			DishID:                 item.DishID,
			Quantity:               item.Quantity,
			ExpectedUnitPriceMinor: item.ExpectedUnitPriceMinor,
		}
	}

	order, err := h.orders.Create(ctx, service.CreateOrderInput{
		EstablishmentID:   request.Body.EstablishmentID,
		TrackingTokenHash: trackingTokenHash,
		CustomerName:      request.Body.CustomerName,
		CustomerPhone:     request.Body.CustomerPhone,
		DeliveryAddress:   request.Body.DeliveryAddress,
		Comment:           request.Body.Comment,
		Items:             items,
	})
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.CreateOrder400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректные данные заказа"),
		}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.CreateOrder404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}

	var orderConflict *service.OrderConflictError
	if errors.As(err, &orderConflict) {
		return api.CreateOrder409JSONResponse{
			Code:      api.OrderConflictResponseCodeOrderConflict,
			Message:   "Заказ нельзя создать с текущим состоянием меню",
			Conflicts: dishConflictsToAPI(orderConflict.Conflicts),
		}, nil
	}
	if err != nil {
		h.logError("create order", err)
		return api.CreateOrder500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.CreateOrder201JSONResponse{
		TrackingToken: trackingToken,
		Status:        api.CreatedOrderStatusPendingConfirmation,
		TotalMinor:    order.TotalMinor,
		CreatedAt:     order.CreatedAt,
	}, nil
}

func (h *Handler) GetTrackedOrder(
	ctx context.Context,
	_ api.GetTrackedOrderRequestObject,
) (api.GetTrackedOrderResponseObject, error) {
	trackingTokenHash, ok := trackingTokenHashFromContext(ctx)
	if !ok {
		return api.GetTrackedOrder401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}

	order, establishment, err := h.orders.GetTracked(ctx, trackingTokenHash)
	if errors.Is(err, domain.ErrUnauthorized) {
		return api.GetTrackedOrder401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	if err != nil {
		h.logError("get tracked order", err)
		return api.GetTrackedOrder500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.GetTrackedOrder200JSONResponse(trackedOrderToAPI(order, establishment)), nil
}

func (h *Handler) CancelOrder(
	ctx context.Context,
	_ api.CancelOrderRequestObject,
) (api.CancelOrderResponseObject, error) {
	trackingTokenHash, ok := trackingTokenHashFromContext(ctx)
	if !ok {
		return api.CancelOrder401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}

	order, err := h.orders.Cancel(ctx, trackingTokenHash)
	if errors.Is(err, domain.ErrUnauthorized) {
		return api.CancelOrder401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	if errors.Is(err, domain.ErrCancellationNotAllowed) {
		return api.CancelOrder409JSONResponse{
			ConflictJSONResponse: conflict(
				api.ErrorResponseCodeCancellationNotAllowed,
				"Заказ уже нельзя отменить",
			),
		}, nil
	}
	if err != nil {
		h.logError("cancel order", err)
		return api.CancelOrder500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.CancelOrder200JSONResponse{
		Status:    api.CancelledOrderStatusCancelled,
		UpdatedAt: order.UpdatedAt,
	}, nil
}

func (h *Handler) ListPartnerOrders(
	ctx context.Context,
	request api.ListPartnerOrdersRequestObject,
) (api.ListPartnerOrdersResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.ListPartnerOrders401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}

	status := domain.OrderStatusPendingConfirmation
	limit := int32(20)
	if request.Params.Status != nil {
		status = domain.OrderStatus(*request.Params.Status)
	}
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}

	orders, total, err := h.orders.ListPartnerOrders(ctx, establishmentID, status, limit)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.ListPartnerOrders400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректные параметры списка заказов"),
		}, nil
	}
	if err != nil {
		h.logError("list partner orders", err)
		return api.ListPartnerOrders500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.ListPartnerOrders200JSONResponse{
		Orders: partnerOrdersToAPI(orders),
		Total:  total,
	}, nil
}

func (h *Handler) GetPartnerOrder(
	ctx context.Context,
	request api.GetPartnerOrderRequestObject,
) (api.GetPartnerOrderResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.GetPartnerOrder401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}

	order, err := h.orders.GetPartnerOrder(ctx, establishmentID, request.OrderID)
	if errors.Is(err, domain.ErrNotFound) {
		return api.GetPartnerOrder404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if err != nil {
		h.logError("get partner order", err)
		return api.GetPartnerOrder500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.GetPartnerOrder200JSONResponse(partnerOrderToAPI(order)), nil
}

func (h *Handler) UpdatePartnerOrderStatus(
	ctx context.Context,
	request api.UpdatePartnerOrderStatusRequestObject,
) (api.UpdatePartnerOrderStatusResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.UpdatePartnerOrderStatus401JSONResponse{
			UnauthorizedJSONResponse: unauthorized(),
		}, nil
	}
	if request.Body == nil {
		return api.UpdatePartnerOrderStatus400JSONResponse{
			BadRequestJSONResponse: badRequest("Тело запроса обязательно"),
		}, nil
	}

	var rejectionReason *domain.RejectionReason
	if request.Body.RejectionReason != nil {
		converted := domain.RejectionReason(*request.Body.RejectionReason)
		rejectionReason = &converted
	}

	order, err := h.orders.UpdatePartnerStatus(
		ctx,
		establishmentID,
		request.OrderID,
		domain.OrderStatus(request.Body.Status),
		rejectionReason,
		request.Body.RejectionComment,
	)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.UpdatePartnerOrderStatus400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректные данные статуса"),
		}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.UpdatePartnerOrderStatus404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if errors.Is(err, domain.ErrInvalidStatusTransition) {
		return api.UpdatePartnerOrderStatus409JSONResponse{
			ConflictJSONResponse: conflict(
				api.ErrorResponseCodeInvalidStatusTransition,
				"Недопустимый переход статуса",
			),
		}, nil
	}
	if err != nil {
		h.logError("update partner order status", err)
		return api.UpdatePartnerOrderStatus500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.UpdatePartnerOrderStatus200JSONResponse{
		ID:               order.ID,
		Status:           api.OrderStatus(order.Status),
		RejectionReason:  rejectionReasonToAPI(order.RejectionReason),
		RejectionComment: order.RejectionComment,
		UpdatedAt:        order.UpdatedAt,
	}, nil
}
