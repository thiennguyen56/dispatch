package domain

import "time"

type DeliveryStatus string

const (
	DeliveryStatusPending    DeliveryStatus = "pending"
	DeliveryStatusInProgress DeliveryStatus = "in_progress"
	DeliveryStatusDelivered  DeliveryStatus = "delivered"
	DeliveryStatusDeadLetter DeliveryStatus = "dead_letter"
)

type Delivery struct {
	ID             string            `json:"id"`
	URL            string            `json:"url"`
	Payload        string            `json:"payload"`
	Headers        map[string]string `json:"headers"`
	Status         DeliveryStatus    `json:"status"`
	AttemptsMade   int               `json:"attempts_made"`
	MaxAttempts    int               `json:"max_attempts"`
	NextAttemptAt  time.Time         `json:"next_attempt_at"`
	LeaseToken     *string           `json:"lease_token"`
	LeaseExpiresAt *time.Time        `json:"lease_expires_at"`
	LastError      *string           `json:"last_error"`
	IdempotencyKey *string           `json:"idempotency_key"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	DeliveredAt    *time.Time        `json:"delivered_at"`
}
