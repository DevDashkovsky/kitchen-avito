package worker

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"kitchen-api/internal/establishment/kitchenapi"
)

const orderBatchSize int32 = 100

type kitchenClient interface {
	ListPartnerOrdersWithResponse(
		context.Context,
		*kitchenapi.ListPartnerOrdersParams,
		...kitchenapi.RequestEditorFn,
	) (*kitchenapi.ListPartnerOrdersResponse, error)
	UpdatePartnerOrderStatusWithResponse(
		context.Context,
		kitchenapi.OrderID,
		kitchenapi.UpdatePartnerOrderStatusJSONRequestBody,
		...kitchenapi.RequestEditorFn,
	) (*kitchenapi.UpdatePartnerOrderStatusResponse, error)
}

type statusTransition struct {
	from kitchenapi.OrderStatus
	to   kitchenapi.UpdateOrderStatusRequestStatus
}

var happyPathTransitions = []statusTransition{
	{
		from: kitchenapi.OrderStatusDelivering,
		to:   kitchenapi.UpdateOrderStatusRequestStatusCompleted,
	},
	{
		from: kitchenapi.OrderStatusReadyForDelivery,
		to:   kitchenapi.UpdateOrderStatusRequestStatusDelivering,
	},
	{
		from: kitchenapi.OrderStatusPreparing,
		to:   kitchenapi.UpdateOrderStatusRequestStatusReadyForDelivery,
	},
	{
		from: kitchenapi.OrderStatusConfirmed,
		to:   kitchenapi.UpdateOrderStatusRequestStatusPreparing,
	},
	{
		from: kitchenapi.OrderStatusPendingConfirmation,
		to:   kitchenapi.UpdateOrderStatusRequestStatusConfirmed,
	},
}

type OrderProcessor struct {
	client   kitchenClient
	interval time.Duration
	logger   *slog.Logger
}

func NewOrderProcessor(
	client kitchenClient,
	interval time.Duration,
	logger *slog.Logger,
) *OrderProcessor {
	return &OrderProcessor{
		client:   client,
		interval: interval,
		logger:   logger,
	}
}

func (w *OrderProcessor) Run(ctx context.Context) {
	w.process(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *OrderProcessor) process(ctx context.Context) {
	for _, transition := range happyPathTransitions {
		if ctx.Err() != nil {
			return
		}

		orders, err := w.listOrders(ctx, transition.from)
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Error(
					"list partner orders",
					"status",
					transition.from,
					"error",
					err,
				)
			}
			continue
		}

		for _, order := range orders {
			if ctx.Err() != nil {
				return
			}

			if err := w.updateOrder(ctx, order.ID, transition.to); err != nil {
				if ctx.Err() == nil {
					w.logger.Error(
						"update partner order status",
						"order_id",
						order.ID,
						"status",
						transition.to,
						"error",
						err,
					)
				}
				continue
			}

			w.logger.Info(
				"partner order status updated",
				"order_id",
				order.ID,
				"status",
				transition.to,
			)
		}
	}
}

func (w *OrderProcessor) listOrders(
	ctx context.Context,
	status kitchenapi.OrderStatus,
) ([]kitchenapi.PartnerOrder, error) {
	limit := kitchenapi.Limit(orderBatchSize)
	filter := kitchenapi.OrderStatusFilter(status)
	response, err := w.client.ListPartnerOrdersWithResponse(
		ctx,
		&kitchenapi.ListPartnerOrdersParams{
			Status: &filter,
			Limit:  &limit,
		},
	)
	if err != nil {
		return nil, err
	}
	if response == nil || response.StatusCode() != http.StatusOK || response.JSON200 == nil {
		return nil, fmt.Errorf("kitchen API returned status %d", responseStatus(response))
	}

	return response.JSON200.Orders, nil
}

func (w *OrderProcessor) updateOrder(
	ctx context.Context,
	orderID int64,
	status kitchenapi.UpdateOrderStatusRequestStatus,
) error {
	response, err := w.client.UpdatePartnerOrderStatusWithResponse(
		ctx,
		orderID,
		kitchenapi.UpdatePartnerOrderStatusJSONRequestBody{Status: status},
	)
	if err != nil {
		return err
	}
	if response == nil || response.StatusCode() != http.StatusOK || response.JSON200 == nil {
		return fmt.Errorf("kitchen API returned status %d", updateResponseStatus(response))
	}

	return nil
}

func responseStatus(response *kitchenapi.ListPartnerOrdersResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func updateResponseStatus(response *kitchenapi.UpdatePartnerOrderStatusResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}
