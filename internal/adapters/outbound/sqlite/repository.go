package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/thiennguyen56/dispatch/internal/application"
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

	result, err := r.db.ExecContext(ctx,
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
	if err != nil {
		return fmt.Errorf("create delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check delivery insertion: %w", err)
	}

	if affected == 0 {
		return application.ErrIdempotencyKeyExists
	}

	r.logger.Info("created delivery", "id", d.ID)
	return nil
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

func (r *Repository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*domain.Delivery, error) {
	var row deliveryRow
	if err := r.db.QueryRowContext(ctx, getDeliveryByIdempotencyKey, idempotencyKey).Scan(
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
		r.logger.Error("get delivery by idempotency key", "idempotencyKey", idempotencyKey, "error", err)
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

func (r *Repository) ClaimNext(ctx context.Context, now time.Time, leaseDuration time.Duration) (*domain.Delivery, error) {
	var delRow deliveryRow
	row := r.db.QueryRowContext(
		ctx,
		claimNextDelivery,
		sql.Named("now", formatTime(now)),
		sql.Named("lease_token", uuid.NewString()),
		sql.Named("lease_expires_at", formatTime(now.Add(leaseDuration))),
	)

	err := row.Scan(
		&delRow.ID,
		&delRow.URL,
		&delRow.Payload,
		&delRow.HeadersJSON,
		&delRow.Status,
		&delRow.AttemptsMade,
		&delRow.MaxAttempts,
		&delRow.NextAttemptAt,
		&delRow.LeaseToken,
		&delRow.LeaseExpiresAt,
		&delRow.LastError,
		&delRow.IdempotencyKey,
		&delRow.CreatedAt,
		&delRow.UpdatedAt,
		&delRow.DeliveredAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, application.ErrNoJob{}
	}

	if err != nil {
		return nil, fmt.Errorf("claim next delivery: %w", err)
	}

	return delRow.toDomain()
}

func (r *Repository) FinalizeAttempt(
	ctx context.Context,
	input application.FinalizeAttemptInput,
	now time.Time,
) error {
	if err := input.Validate(); err != nil {
		return fmt.Errorf("validate finalization: %w", err)
	}
	if now.IsZero() || now.Before(input.Result.FinishedAt) {
		return errors.New("finalization time must not precede attempt completion")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finalization: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var attemptNumber int
	err = tx.QueryRowContext(
		ctx,
		finalizeDelivery,
		sql.Named("delivery_id", input.DeliveryID),
		sql.Named("lease_token", input.LeaseToken),
		sql.Named("outcome", string(input.Result.Outcome)),
		sql.Named("retry_at", formatOptionalTime(input.RetryAt)),
		sql.Named("finished_at", formatTime(input.Result.FinishedAt)),
		sql.Named("error_message", input.ErrorMessage),
		sql.Named("now", formatTime(now)),
	).Scan(&attemptNumber)

	if errors.Is(err, sql.ErrNoRows) {
		return application.ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("update delivery during finalization: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		insertCompletedAttempt,
		sql.Named("delivery_id", input.DeliveryID),
		sql.Named("attempt_number", attemptNumber),
		sql.Named("started_at", formatTime(input.Result.StartedAt)),
		sql.Named("finished_at", formatTime(input.Result.FinishedAt)),
		sql.Named("outcome", string(input.Result.Outcome)),
		sql.Named("response_status", input.Result.ResponseStatus),
		sql.Named("error_message", input.ErrorMessage),
		sql.Named("duration_ms", input.Result.Duration.Milliseconds()),
	)
	if err != nil {
		return fmt.Errorf("insert completed attempt: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit finalization: %w", err)
	}

	return nil
}

// Fixed-width UTC timestamps preserve chronological order in SQLite TEXT comparisons.
const dbTimeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(value time.Time) string {
	return value.UTC().Format(dbTimeLayout)
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
