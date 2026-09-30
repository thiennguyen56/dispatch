package application

import (
	"errors"
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

type InputSubmit struct {
	URL     string            `json:"url"`
	Payload string            `json:"payload"`
	Headers map[string]string `json:"headers"`
}

// SendResult describes one outbound attempt without assigning persistence IDs
// or deciding whether the delivery should be retried.
type SendResult struct {
	StartedAt      time.Time
	FinishedAt     time.Time
	Duration       time.Duration
	Outcome        domain.AttemptOutcome
	ResponseStatus *int
}

type FinalizeAttemptInput struct {
	DeliveryID string
	LeaseToken string

	Result       SendResult
	ErrorMessage *string
	RetryAt      *time.Time
}

func (input FinalizeAttemptInput) Validate() error {
	if input.DeliveryID == "" || input.LeaseToken == "" {
		return errors.New("delivery ID and lease token are required")
	}

	result := input.Result
	if result.StartedAt.IsZero() || result.FinishedAt.IsZero() {
		return errors.New("attempt timestamps are required")
	}
	if result.FinishedAt.Before(result.StartedAt) || result.Duration < 0 {
		return errors.New("invalid attempt timing")
	}

	if result.ResponseStatus != nil {
		status := *result.ResponseStatus
		if status < 100 || status > 599 {
			return errors.New("invalid response status")
		}
	}

	switch result.Outcome {
	case domain.AttemptOutcomeSucceeded:
		if result.ResponseStatus == nil ||
			*result.ResponseStatus < 200 ||
			*result.ResponseStatus >= 300 {
			return errors.New("successful attempt requires a 2xx response")
		}
		if input.RetryAt != nil || input.ErrorMessage != nil {
			return errors.New("successful attempt cannot have retry or error data")
		}

	case domain.AttemptOutcomeFailed, domain.AttemptOutcomeTimedOut:
		if result.ResponseStatus != nil &&
			*result.ResponseStatus >= 200 &&
			*result.ResponseStatus < 300 {
			return errors.New("failed attempt cannot have a 2xx response")
		}
		if input.RetryAt != nil &&
			input.RetryAt.Before(result.FinishedAt) {
			return errors.New("retry cannot precede attempt completion")
		}

	default:
		return errors.New("invalid attempt outcome")
	}

	return nil
}
