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
	ID             string
	URL            string
	Payload        string
	Headers        map[string]string
	Status         DeliveryStatus
	AttemptsMade   int
	MaxAttempts    int
	NextAttemptAt  time.Time
	LeaseToken     *string
	LeaseExpiresAt *time.Time
	LastError      *string
	IdempotencyKey *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeliveredAt    *time.Time
}
