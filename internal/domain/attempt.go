package domain

import "time"

type AttemptOutcome string

const (
	AttemptOutcomeSucceeded AttemptOutcome = "succeeded"
	AttemptOutcomeFailed    AttemptOutcome = "failed"
	AttemptOutcomeTimedOut  AttemptOutcome = "timed_out"
)

// Attempt records one outbound HTTP request for a delivery.
type Attempt struct {
	ID             int64           `json:"id"`
	DeliveryID     string          `json:"delivery_id"`
	AttemptNumber  int             `json:"attempt_number"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
	Outcome        *AttemptOutcome `json:"outcome"`
	ResponseStatus *int            `json:"response_status"`
	ErrorMessage   *string         `json:"error_message"`
	Duration       time.Duration   `json:"duration"`
}
