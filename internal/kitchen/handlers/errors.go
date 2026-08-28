package handlers

import "kitchen-api/internal/kitchen/api"

func badRequest(message string) api.BadRequestJSONResponse {
	return api.BadRequestJSONResponse{
		Code:    api.ValidationErrorResponseCodeValidationError,
		Message: message,
		Fields:  []api.FieldError{},
	}
}

func unauthorized() api.UnauthorizedJSONResponse {
	return api.UnauthorizedJSONResponse(errorResponse(
		api.ErrorResponseCodeUnauthorized,
		"Неверные данные доступа",
	))
}

func notFound() api.NotFoundJSONResponse {
	return api.NotFoundJSONResponse(errorResponse(
		api.ErrorResponseCodeNotFound,
		"Ресурс не найден",
	))
}

func internalError() api.InternalErrorJSONResponse {
	return api.InternalErrorJSONResponse(errorResponse(
		api.ErrorResponseCodeInternalError,
		"Внутренняя ошибка сервиса",
	))
}

func conflict(code api.ErrorResponseCode, message string) api.ConflictJSONResponse {
	return api.ConflictJSONResponse(errorResponse(code, message))
}

func errorResponse(code api.ErrorResponseCode, message string) api.ErrorResponse {
	return api.ErrorResponse{Code: code, Message: message}
}
