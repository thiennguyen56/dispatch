package application

import (
	"context"
	"log/slog"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type Service struct {
	logger *slog.Logger
	repo   DeliveryRepository
}

func NewService(logger *slog.Logger, repo DeliveryRepository) *Service {
	return &Service{logger: logger, repo: repo}
}

func (s *Service) Submit(ctx context.Context, input InputSubmit) (*domain.Delivery, error) {
	deliveryRecord := newDelivery(input, uuid.NewString(), time.Now())
	err := s.repo.Create(ctx, deliveryRecord)
	if err != nil {
		s.logger.Error("failed to create delivery", "error", err)
		return nil, err
	}
	return &deliveryRecord, nil
}

func newDelivery(input InputSubmit, id string, now time.Time) domain.Delivery {
	return domain.Delivery{
		ID:            id,
		URL:           input.URL,
		Payload:       input.Payload,
		Headers:       maps.Clone(input.Headers),
		Status:        domain.DeliveryStatusPending,
		AttemptsMade:  0,
		MaxAttempts:   8,
		NextAttemptAt: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Delivery, error) {
	delivery, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return delivery, nil
}
