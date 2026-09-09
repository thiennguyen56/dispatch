package sqlite

import "database/sql"

type deliveryRow struct {
	ID             string
	URL            string
	Payload        string
	HeadersJSON    string
	Status         string
	AttemptsMade   int
	MaxAttempts    int
	NextAttemptAt  string
	LeaseToken     sql.NullString
	LeaseExpiresAt sql.NullString
	LastError      sql.NullString
	IdempotencyKey sql.NullString
	CreatedAt      string
	UpdatedAt      string
	DeliveredAt    sql.NullString
}
