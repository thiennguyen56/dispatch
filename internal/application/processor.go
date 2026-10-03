package application

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

const (
	leaseDuration   = 30 * time.Second
	finalizeTimeout = 5 * time.Second
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

	p.logger.InfoContext(ctx, "claimed delivery",
		"delivery_id", claim.ID,
		"attempt_number", claim.AttemptsMade+1,
	)
	sent, err := p.sender.Send(ctx, *claim)
	p.logger.Info("sent", "sent", sent)

	finalizeAttemptInput := FinalizeAttemptInput{
		DeliveryID: claim.ID,
		LeaseToken: *claim.LeaseToken,
		Result:     sent,
	}
	if sent.Outcome != domain.AttemptOutcomeSucceeded {
		message := "webhook request failed"
		retryable := false

		if sent.ResponseStatus != nil {
			status := *sent.ResponseStatus
			message = fmt.Sprintf("webhook request failed with status %d", status)
			retryable = status == http.StatusRequestTimeout ||
				status == http.StatusTooManyRequests ||
				status >= 500
		} else if err != nil {
			message = "webhook transport failed"
			if sent.Outcome == domain.AttemptOutcomeTimedOut {
				message = "webhook request timed out"
			}
			retryable = true
		}

		finalizeAttemptInput.ErrorMessage = &message
		if retryable {
			retryAt := sent.FinishedAt.Add(time.Minute)
			finalizeAttemptInput.RetryAt = &retryAt
		}
	}

	if err := finalizeAttemptInput.Validate(); err != nil {
		p.logger.Error("invalid finalizeAttemptInput", "error", err)
		return err
	}

	// let finalize attempt run to completion, but don't block the processor when the parent ctx is cancelled
	finalizeCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		finalizeTimeout,
	)
	defer cancel()

	if err := p.repo.FinalizeAttempt(finalizeCtx, finalizeAttemptInput, time.Now()); err != nil {
		p.logger.Error("failed to finalize attempt", "error", err)
		return err
	}

	p.logger.InfoContext(ctx, "webhook attempt completed",
		"delivery_id", claim.ID,
		"outcome", sent.Outcome,
		"duration", sent.Duration,
	)
	return nil
}
