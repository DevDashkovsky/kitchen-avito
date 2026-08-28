package handlers

import (
	"context"
	"errors"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/domain"
	"kitchen-api/internal/kitchen/service"
)

func (h *Handler) GetEstablishmentMenu(
	ctx context.Context,
	request api.GetEstablishmentMenuRequestObject,
) (api.GetEstablishmentMenuResponseObject, error) {
	menu, err := h.menu.GetMenu(ctx, request.EstablishmentID)
	if errors.Is(err, domain.ErrNotFound) {
		return api.GetEstablishmentMenu404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if err != nil {
		h.logError("get establishment menu", err)
		return api.GetEstablishmentMenu500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.GetEstablishmentMenu200JSONResponse(menuToAPI(menu)), nil
}

func (h *Handler) GetPartnerMenu(
	ctx context.Context,
	_ api.GetPartnerMenuRequestObject,
) (api.GetPartnerMenuResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.GetPartnerMenu401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}

	menu, err := h.menu.GetMenu(ctx, establishmentID)
	if err != nil {
		h.logError("get partner menu", err)
		return api.GetPartnerMenu500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.GetPartnerMenu200JSONResponse(menuToAPI(menu)), nil
}

func (h *Handler) CreateCategory(
	ctx context.Context,
	request api.CreateCategoryRequestObject,
) (api.CreateCategoryResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.CreateCategory401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	if request.Body == nil {
		return api.CreateCategory400JSONResponse{
			BadRequestJSONResponse: badRequest("Тело запроса обязательно"),
		}, nil
	}

	category, err := h.menu.CreateCategory(ctx, establishmentID, request.Body.Name)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.CreateCategory400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректное название категории"),
		}, nil
	}
	if errors.Is(err, domain.ErrConflict) {
		return api.CreateCategory409JSONResponse{
			ConflictJSONResponse: conflict(
				api.ErrorResponseCodeDuplicateCategory,
				"Категория с таким названием уже существует",
			),
		}, nil
	}
	if err != nil {
		h.logError("create category", err)
		return api.CreateCategory500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.CreateCategory201JSONResponse(categoryToAPI(category)), nil
}

func (h *Handler) UpdateCategory(
	ctx context.Context,
	request api.UpdateCategoryRequestObject,
) (api.UpdateCategoryResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.UpdateCategory401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	if request.Body == nil {
		return api.UpdateCategory400JSONResponse{
			BadRequestJSONResponse: badRequest("Тело запроса обязательно"),
		}, nil
	}

	category, err := h.menu.UpdateCategory(
		ctx,
		establishmentID,
		request.CategoryID,
		request.Body.Name,
	)
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.UpdateCategory400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректное название категории"),
		}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.UpdateCategory404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if errors.Is(err, domain.ErrConflict) {
		return api.UpdateCategory409JSONResponse{
			ConflictJSONResponse: conflict(
				api.ErrorResponseCodeDuplicateCategory,
				"Категория с таким названием уже существует",
			),
		}, nil
	}
	if err != nil {
		h.logError("update category", err)
		return api.UpdateCategory500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.UpdateCategory200JSONResponse(categoryToAPI(category)), nil
}

func (h *Handler) CreateDish(
	ctx context.Context,
	request api.CreateDishRequestObject,
) (api.CreateDishResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.CreateDish401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	if request.Body == nil {
		return api.CreateDish400JSONResponse{
			BadRequestJSONResponse: badRequest("Тело запроса обязательно"),
		}, nil
	}

	description := ""
	isAvailable := false
	if request.Body.Description != nil {
		description = *request.Body.Description
	}
	if request.Body.IsAvailable != nil {
		isAvailable = *request.Body.IsAvailable
	}

	dish, err := h.menu.CreateDish(ctx, establishmentID, service.NewDish{
		CategoryID:  request.Body.CategoryID,
		Name:        request.Body.Name,
		Description: description,
		PriceMinor:  request.Body.PriceMinor,
		IsAvailable: isAvailable,
	})
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.CreateDish400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректные данные блюда"),
		}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.CreateDish404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if err != nil {
		h.logError("create dish", err)
		return api.CreateDish500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.CreateDish201JSONResponse(dishToAPI(dish)), nil
}

func (h *Handler) UpdateDish(
	ctx context.Context,
	request api.UpdateDishRequestObject,
) (api.UpdateDishResponseObject, error) {
	establishmentID, ok := establishmentIDFromContext(ctx)
	if !ok {
		return api.UpdateDish401JSONResponse{UnauthorizedJSONResponse: unauthorized()}, nil
	}
	if request.Body == nil {
		return api.UpdateDish400JSONResponse{
			BadRequestJSONResponse: badRequest("Тело запроса обязательно"),
		}, nil
	}

	dish, err := h.menu.UpdateDish(ctx, establishmentID, request.DishID, service.DishPatch{
		CategoryID:  request.Body.CategoryID,
		Name:        request.Body.Name,
		Description: request.Body.Description,
		PriceMinor:  request.Body.PriceMinor,
		IsAvailable: request.Body.IsAvailable,
	})
	if errors.Is(err, domain.ErrInvalidInput) {
		return api.UpdateDish400JSONResponse{
			BadRequestJSONResponse: badRequest("Некорректные данные блюда"),
		}, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return api.UpdateDish404JSONResponse{NotFoundJSONResponse: notFound()}, nil
	}
	if err != nil {
		h.logError("update dish", err)
		return api.UpdateDish500JSONResponse{
			InternalErrorJSONResponse: internalError(),
		}, nil
	}

	return api.UpdateDish200JSONResponse(dishToAPI(dish)), nil
}
