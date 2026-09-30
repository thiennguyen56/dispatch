package application

import (
	"context"
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

type DeliveryRepository interface {
	Create(ctx context.Context, delivery domain.Delivery) error
	Get(ctx context.Context, id string) (*domain.Delivery, error)
	ClaimNext(ctx context.Context, now time.Time, leaseDuration time.Duration) (*domain.Delivery, error)

	FinalizeAttempt(
		ctx context.Context,
		input FinalizeAttemptInput,
		now time.Time,
	) error
}

type Sender interface {
	// Send performs one outbound attempt. A nil error means an HTTP response
	// was received; callers must inspect Outcome, including for non-2xx replies.
	// Errors describe request/transport failures; the result still includes timing
	// and an outcome, with no response status when no response was received.
	Send(ctx context.Context, delivery domain.Delivery) (SendResult, error)
}
