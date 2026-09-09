package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

type Repository struct {
	logger *slog.Logger
	db     *sql.DB
}

func NewRepository(db *sql.DB, logger *slog.Logger) *Repository {
	return &Repository{db: db, logger: logger}
}

func (r *Repository) Create(
	ctx context.Context,
	d domain.Delivery,
) error {
	headersJSON, err := json.Marshal(d.Headers)
	if err != nil {
		return fmt.Errorf("marshal delivery headers: %w", err)
	}
	if d.Headers == nil {
		headersJSON = []byte("{}")
	}

	_, err = r.db.ExecContext(ctx,
		insertDelivery,
		d.ID,
		d.URL,
		d.Payload,
		string(headersJSON),
		string(d.Status),
		d.AttemptsMade,
		d.MaxAttempts,
		formatTime(d.NextAttemptAt),
		d.LeaseToken,
		formatOptionalTime(d.LeaseExpiresAt),
		d.LastError,
		d.IdempotencyKey,
		formatTime(d.CreatedAt),
		formatTime(d.UpdatedAt),
		formatOptionalTime(d.DeliveredAt),
	)
	r.logger.Info("created delivery", "id", d.ID)
	return err
}

func (r *Repository) Get(ctx context.Context, id string) (*domain.Delivery, error) {
	var row deliveryRow
	if err := r.db.QueryRowContext(ctx, getDelivery, id).Scan(
		&row.ID,
		&row.URL,
		&row.Payload,
		&row.HeadersJSON,
		&row.Status,
		&row.AttemptsMade,
		&row.MaxAttempts,
		&row.NextAttemptAt,
		&row.LeaseToken,
		&row.LeaseExpiresAt,
		&row.LastError,
		&row.IdempotencyKey,
		&row.CreatedAt,
		&row.UpdatedAt,
		&row.DeliveredAt,
	); err != nil {
		r.logger.Error("get delivery", "id", id, "error", err)
		return nil, err
	}

	delivery, err := row.toDomain()
	if err != nil {
		return nil, fmt.Errorf("map delivery row: %w", err)
	}
	return delivery, nil
}

func (row deliveryRow) toDomain() (*domain.Delivery, error) {
	var headers map[string]string
	if err := json.Unmarshal([]byte(row.HeadersJSON), &headers); err != nil {
		return nil, fmt.Errorf("unmarshal headers: %w", err)
	}

	nextAttemptAt, err := parseTime(row.NextAttemptAt)
	if err != nil {
		return nil, fmt.Errorf("parse next attempt time: %w", err)
	}
	createdAt, err := parseTime(row.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse creation time: %w", err)
	}
	updatedAt, err := parseTime(row.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse update time: %w", err)
	}
	leaseExpiresAt, err := parseOptionalTime(row.LeaseExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("parse lease expiry: %w", err)
	}
	deliveredAt, err := parseOptionalTime(row.DeliveredAt)
	if err != nil {
		return nil, fmt.Errorf("parse delivery time: %w", err)
	}

	return &domain.Delivery{
		ID:             row.ID,
		URL:            row.URL,
		Payload:        row.Payload,
		Headers:        headers,
		Status:         domain.DeliveryStatus(row.Status),
		AttemptsMade:   row.AttemptsMade,
		MaxAttempts:    row.MaxAttempts,
		NextAttemptAt:  nextAttemptAt,
		LeaseToken:     optionalString(row.LeaseToken),
		LeaseExpiresAt: leaseExpiresAt,
		LastError:      optionalString(row.LastError),
		IdempotencyKey: optionalString(row.IdempotencyKey),
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		DeliveredAt:    deliveredAt,
	}, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}

	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
