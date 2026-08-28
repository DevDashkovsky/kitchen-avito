package handlers

import (
	"context"
	"errors"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
)

func (h *Handler) ListEstablishments(
	ctx context.Context,
	request api.ListEstablishmentsRequestObject,
) (api.ListEstablishmentsResponseObject, error) {
	search := ""
	limit := int32(20)
	offset := int64(0)
	if request.Params.Query != nil {
		search = *request.Params.Query
	}
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
	}

	establishments, total, err := h.establishments.List(ctx, search, limit, offset)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.ListEstablishments400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректные параметры списка"),
		}, nil
	}
	if err != nil {
		h.logError("list establishments", err)
		return api.ListEstablishments500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.ListEstablishments200JSONResponse{
		Establishments: establishmentsToAPI(establishments),
		Limit:          limit,
		Offset:         offset,
		Total:          total,
	}, nil
}

func (h *Handler) UpdateEstablishment(
	ctx context.Context,
	request api.UpdateEstablishmentRequestObject,
) (api.UpdateEstablishmentResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.UpdateEstablishment401JSONResponse{
			UnauthorizedJSONResponse: unauthorized(),
		}, nil
	}
	if request.Body == nil {
		return api.UpdateEstablishment400JSONResponse{
			BadRequestJSONResponse: badRequest("Тело запроса обязательно"),
		}, nil
	}

	establishment, err := h.establishments.UpdateAcceptsOrders(
		ctx,
		establishmentID,
		request.Body.AcceptsOrders,
	)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.UpdateEstablishment400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректные данные заведения"),
		}, nil
	}
	if err != nil {
		h.logError("update establishment", err)
		return api.UpdateEstablishment500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.UpdateEstablishment200JSONResponse(establishmentToAPI(establishment)), nil
}
