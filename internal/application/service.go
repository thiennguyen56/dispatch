package application

import (
	"log/slog"
)

type Service struct {
	logger *slog.Logger
	repo   DeliveryRepository
}

func NewService(logger *slog.Logger, repo DeliveryRepository) *Service {
	return &Service{logger: logger, repo: repo}
}
