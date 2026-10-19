package application

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type Service struct {
	logger *slog.Logger
	repo   DeliveryRepository
}

func NewService(logger *slog.Logger, repo DeliveryRepository) *Service {
	return &Service{logger: logger, repo: repo}
}

func (s *Service) Submit(ctx context.Context, input InputSubmit) (*domain.Delivery, error) {
	deliveryRecord := newDelivery(input, uuid.NewString(), time.Now())
	if input.IdempotencyKey != nil {
		delivery, err := s.repo.GetByIdempotencyKey(ctx, *input.IdempotencyKey)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.logger.Warn("failed to get delivery by idempotency key", "idempotencyKey", *input.IdempotencyKey, "error", err)
			return nil, err
		}
		if delivery != nil {
			return delivery, nil
		}
	}
	err := s.repo.Create(ctx, deliveryRecord)
	if errors.Is(err, ErrIdempotencyKeyExists) &&
		input.IdempotencyKey != nil {
		return s.repo.GetByIdempotencyKey(ctx, *input.IdempotencyKey)
	}
	if err != nil {
		return nil, err
	}

	return &deliveryRecord, nil
}

func newDelivery(input InputSubmit, id string, now time.Time) domain.Delivery {
	return domain.Delivery{
		ID:             id,
		URL:            input.URL,
		Payload:        input.Payload,
		Headers:        maps.Clone(input.Headers),
		Status:         domain.DeliveryStatusPending,
		AttemptsMade:   0,
		MaxAttempts:    8,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
		IdempotencyKey: input.IdempotencyKey,
	}
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Delivery, error) {
	delivery, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return delivery, nil
}

func (s *Service) ListAttempts(ctx context.Context, deliveryID string) ([]*domain.Attempt, error) {
	attempts, err := s.repo.ListAttempts(ctx, deliveryID)
	if err != nil {
		s.logger.Error("failed to list attempts from repo level", "error", err)
		return nil, err
	}

	return attempts, nil
}

func (s *Service) ListDeliveries(ctx context.Context, query ListDeliveriesQuery) (*DeliveryPage, error) {
	if query.Limit == 0 || query.Limit > 100 {
		query.Limit = 20
	}

	if query.CursorRaw != nil && *query.CursorRaw != "" {
		cursor, err := decodeCursor(*query.CursorRaw)
		if err != nil {
			return nil, err
		}
		query.Cursor = cursor
	}
	if query.Cursor != nil {
		cursor, err := query.Cursor.normalize(query.Status)
		if err != nil {
			return nil, err
		}
		query.Cursor = cursor
	}

	return s.repo.ListDeliveries(ctx, query)
}

func decodeCursor(raw string) (*Cursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: decode base64: %v", ErrInvalidCursor, err)
	}
	var cursor *Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, fmt.Errorf("%w: decode JSON: %v", ErrInvalidCursor, err)
	}
	if cursor == nil {
		return nil, ErrInvalidCursor
	}
	return cursor, nil
}

// normalize validates the page boundary and returns a copy suitable for SQLite.
func (c *Cursor) normalize(status *domain.DeliveryStatus) (*Cursor, error) {
	if c.ID == nil || strings.TrimSpace(*c.ID) == "" || c.CreatedAt == nil {
		return nil, ErrInvalidCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, *c.CreatedAt)
	if err != nil || createdAt.IsZero() {
		return nil, fmt.Errorf("%w: invalid creation time", ErrInvalidCursor)
	}
	if c.Status != nil {
		switch *c.Status {
		case domain.DeliveryStatusPending, domain.DeliveryStatusInProgress,
			domain.DeliveryStatusDelivered, domain.DeliveryStatusDeadLetter:
		default:
			return nil, fmt.Errorf("%w: invalid status", ErrInvalidCursor)
		}
	}
	// A cursor belongs to the same filter that produced it, including no filter.
	if (c.Status == nil) != (status == nil) {
		return nil, fmt.Errorf("%w: status filter changed", ErrInvalidCursor)
	}
	if c.Status != nil && *c.Status != *status {
		return nil, fmt.Errorf("%w: status filter changed", ErrInvalidCursor)
	}
	// SQLite compares fixed-width UTC timestamp strings.
	normalized := createdAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
	return &Cursor{ID: c.ID, CreatedAt: &normalized, Status: c.Status}, nil
}
