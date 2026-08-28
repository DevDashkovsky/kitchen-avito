package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"kitchen-api/internal/kitchen/domain"
)

type establishmentRepository interface {
	GetEstablishmentByAPIKeyHash(context.Context, []byte) (domain.Establishment, error)
	ListEstablishments(context.Context, string, int32, int64) ([]domain.Establishment, int64, error)
	UpdateEstablishmentAcceptsOrders(context.Context, int64, bool) (domain.Establishment, error)
}

type EstablishmentService struct {
	repository establishmentRepository
}

func NewEstablishmentService(repository establishmentRepository) *EstablishmentService {
	return &EstablishmentService{repository: repository}
}

func (s *EstablishmentService) Authenticate(
	ctx context.Context,
	apiKeyHash []byte,
) (int64, error) {
	if len(apiKeyHash) == 0 {
		return 0, domain.ErrUnauthorized
	}

	establishment, err := s.repository.GetEstablishmentByAPIKeyHash(ctx, apiKeyHash)
	if errors.Is(err, domain.ErrNotFound) {
		return 0, domain.ErrUnauthorized
	}
	if err != nil {
		return 0, fmt.Errorf("authenticate establishment: %w", err)
	}

	return establishment.ID, nil
}

func (s *EstablishmentService) List(
	ctx context.Context,
	search string,
	limit int32,
	offset int64,
) ([]domain.Establishment, int64, error) {
	search = strings.TrimSpace(search)
	if limit < 1 || limit > 100 || offset < 0 || len([]rune(search)) > 200 {
		return nil, 0, domain.ErrInvalidInput
	}

	establishments, total, err := s.repository.ListEstablishments(ctx, search, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list establishments: %w", err)
	}

	return establishments, total, nil
}

func (s *EstablishmentService) UpdateAcceptsOrders(
	ctx context.Context,
	establishmentID int64,
	acceptsOrders bool,
) (domain.Establishment, error) {
	establishment, err := s.repository.UpdateEstablishmentAcceptsOrders(
		ctx,
		establishmentID,
		acceptsOrders,
	)
	if err != nil {
		return domain.Establishment{}, fmt.Errorf("update establishment: %w", err)
	}

	return establishment, nil
}
