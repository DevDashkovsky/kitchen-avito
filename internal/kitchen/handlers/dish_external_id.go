package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
)

// The generated decoder may collapse repeated query values. Reject ambiguity
// at the HTTP boundary before decoding the external identifier.
func (h *Handler) ExternalDishQueryGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/partner/dishes/by-external-id" {
			query, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil || len(query["external_id"]) != 1 {
				writeJSON(w, http.StatusBadRequest, badRequest("Нужен один корректный параметр external_id"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) GetPartnerDish(ctx context.Context, request api.GetPartnerDishRequestObject) (api.GetPartnerDishResponseObject, error) {
	venue, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.GetPartnerDish401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	dish, err := h.menu.GetDish(ctx, venue, request.DishID)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.GetPartnerDish400JSONResponse{BadRequestJSONResponse: badRequest("Некорректный ID блюда")}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.GetPartnerDish404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if err != nil {
		h.logError("get partner dish", err)
		return api.GetPartnerDish500JSONResponse{InternalErrorJSONResponse: internalError()}, nil
	}
	return api.GetPartnerDish200JSONResponse(dishToAPI(dish)), nil
}
func (h *Handler) GetPartnerDishByExternalID(ctx context.Context, request api.GetPartnerDishByExternalIDRequestObject) (api.GetPartnerDishByExternalIDResponseObject, error) {
	venue, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.GetPartnerDishByExternalID401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	dish, err := h.menu.GetDishByExternalID(ctx, venue, request.Params.ExternalID)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.GetPartnerDishByExternalID400JSONResponse{BadRequestJSONResponse: badRequest("Некорректный внешний ID")}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.GetPartnerDishByExternalID404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if err != nil {
		h.logError("find partner dish", err)
		return api.GetPartnerDishByExternalID500JSONResponse{InternalErrorJSONResponse: internalError()}, nil
	}
	return api.GetPartnerDishByExternalID200JSONResponse(dishToAPI(dish)), nil
}
func (h *Handler) BindDishExternalID(ctx context.Context, request api.BindDishExternalIDRequestObject) (api.BindDishExternalIDResponseObject, error) {
	venue, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.BindDishExternalID401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	if request.Body == nil {
		return api.BindDishExternalID400JSONResponse{BadRequestJSONResponse: badRequest("Тело запроса обязательно")}, nil
	}
	dish, err := h.menu.BindDishExternalID(ctx, venue, request.DishID, request.Body.ExternalID)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.BindDishExternalID400JSONResponse{BadRequestJSONResponse: badRequest("Некорректный внешний ID")}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.BindDishExternalID404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if errors.Is(err, domain.ErrExternalIDConflict) {
		return api.BindDishExternalID409JSONResponse{ConflictJSONResponse: conflict(api.ErrorResponseCodeExternalIDConflict, "Внешний ID уже занят")}, nil
	}
	if errors.Is(err, domain.ErrExternalIDAlreadyBound) {
		return api.BindDishExternalID409JSONResponse{ConflictJSONResponse: conflict(api.ErrorResponseCodeExternalIDAlreadyBound, "У блюда уже есть другой внешний ID")}, nil
	}
	if err != nil {
		h.logError("bind dish external ID", err)
		return api.BindDishExternalID500JSONResponse{InternalErrorJSONResponse: internalError()}, nil
	}
	return api.BindDishExternalID200JSONResponse(dishToAPI(dish)), nil
}
