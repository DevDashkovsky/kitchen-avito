package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"

	"kitchen-api/internal/kitchen/api"
	"kitchen-api/internal/kitchen/service"
)

type contextKey uint8

const (
	establishmentIDContextKey contextKey = iota
	trackingTokenHashContextKey
)

type Handler struct {
	establishments *service.EstablishmentService
	menu           *service.MenuService
	orders         *service.OrderService
	logger         *slog.Logger
}

func New(
	establishments *service.EstablishmentService,
	menu *service.MenuService,
	orders *service.OrderService,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return &Handler{
		establishments: establishments,
		menu:           menu,
		orders:         orders,
		logger:         logger,
	}
}

func establishmentIDFromContext(ctx context.Context) (int64, bool) {
	establishmentID, ok := ctx.Value(establishmentIDContextKey).(int64)
	return establishmentID, ok && establishmentID > 0
}

func trackingTokenHashFromContext(ctx context.Context) ([]byte, bool) {
	hash, ok := ctx.Value(trackingTokenHashContextKey).([]byte)
	return hash, ok && len(hash) > 0
}

func generateTrackingToken() (string, []byte, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", nil, err
	}

	token := base64.RawURLEncoding.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(token))

	return token, hash[:], nil
}

func (h *Handler) logError(operation string, err error) {
	h.logger.Error("request failed", "operation", operation, "error", err)
}

var _ api.StrictServerInterface = (*Handler)(nil)
