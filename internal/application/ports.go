package application

import (
	"context"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

type DeliveryRepository interface {
	Create(ctx context.Context, delivery domain.Delivery) error
	Get(ctx context.Context, id string) (*domain.Delivery, error)
}

type Sender interface {
	Send(ctx context.Context, delivery domain.Delivery) (any, error)
}
