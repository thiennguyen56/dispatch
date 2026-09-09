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
	ID             int64
	DeliveryID     string
	AttemptNumber  int
	StartedAt      time.Time
	FinishedAt     *time.Time
	Outcome        *AttemptOutcome
	ResponseStatus *int
	ErrorMessage   *string
	Duration       time.Duration
}
