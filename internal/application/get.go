package application

import (
	"context"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

func (s *Service) Get(ctx context.Context, id string) (*domain.Delivery, error) {
	delivery, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return delivery, nil
}
