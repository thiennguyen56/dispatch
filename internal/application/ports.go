package application

import (
	"context"
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

type DeliveryRepository interface {
	Create(ctx context.Context, delivery domain.Delivery) error
	Get(ctx context.Context, id string) (*domain.Delivery, error)
	GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*domain.Delivery, error)

	ListAttempts(ctx context.Context, deliveryID string) ([]*domain.Attempt, error)
	ListDeliveries(ctx context.Context, query ListDeliveriesQuery) (*DeliveryPage, error)
	ClaimNext(ctx context.Context, now time.Time, leaseDuration time.Duration) (*domain.Delivery, error)

	FinalizeAttempt(
		ctx context.Context,
		input FinalizeAttemptInput,
		now time.Time,
	) error
}

type Sender interface {
	Send(ctx context.Context, delivery domain.Delivery) (SendResult, error)
}
