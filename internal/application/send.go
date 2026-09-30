package application

import (
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

// SendResult describes one outbound attempt without assigning persistence IDs
// or deciding whether the delivery should be retried.
type SendResult struct {
	StartedAt      time.Time
	FinishedAt     time.Time
	Duration       time.Duration
	Outcome        domain.AttemptOutcome
	ResponseStatus *int
}
