package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/thiennguyen56/dispatch/internal/application"
)

type Service interface {
	ProcessNext(ctx context.Context) error
}

type Pool struct {
}

func NewPool() *Pool {
	return &Pool{}
}

type Worker struct {
	logger       *slog.Logger
	service      Service
	pollInterval time.Duration
}

func NewWorker(logger *slog.Logger, service Service, pollInterval time.Duration) *Worker {
	return &Worker{
		logger:       logger,
		service:      service,
		pollInterval: pollInterval,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := w.service.ProcessNext(ctx)

		if err == nil {
			continue // Immediately try the next job.
		}

		if !errors.Is(err, application.ErrNoJob{}) {
			w.logger.ErrorContext(ctx, "processing failed",
				"error", err,
			)
		}

		// Pause when idle or after an infrastructure error.
		timer := time.NewTimer(w.pollInterval)
		select {
		case <-ctx.Done():
			w.logger.InfoContext(ctx, "worker stopped", "error", ctx.Err())
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
