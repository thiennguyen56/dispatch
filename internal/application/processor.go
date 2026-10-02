package application

import (
	"context"
	"log/slog"
	"time"
)

const (
	leaseDuration = 10 * time.Second
)

type Processor struct {
	logger *slog.Logger
	sender Sender
	repo   DeliveryRepository
}

func NewProcessor(logger *slog.Logger, repo DeliveryRepository, sender Sender) *Processor {
	return &Processor{logger: logger, repo: repo, sender: sender}
}

func (p *Processor) ProcessNext(ctx context.Context) error {
	now := time.Now()
	p.logger.Info("attempting to claim next job", "now", now)
	claim, err := p.repo.ClaimNext(ctx, now, leaseDuration)
	if err != nil {
		return err
	}

	p.logger.Info("claimed job", "claim", claim)
	return nil
}
