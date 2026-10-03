package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type repositoryStub struct {
	created     domain.Delivery
	createErr   error
	getDelivery *domain.Delivery
	getErr      error
	createCalls int
	lookup      func(context.Context, string) (*domain.Delivery, error)
}

func (r *repositoryStub) Create(_ context.Context, delivery domain.Delivery) error {
	r.createCalls++
	r.created = delivery
	return r.createErr
}

func (r *repositoryStub) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Delivery, error) {
	if r.lookup != nil {
		return r.lookup(ctx, key)
	}
	return nil, sql.ErrNoRows
}

func (r *repositoryStub) Get(context.Context, string) (*domain.Delivery, error) {
	return r.getDelivery, r.getErr
}

func (r *repositoryStub) ClaimNext(_ context.Context, _ time.Time, _ time.Duration) (*domain.Delivery, error) {
	return nil, nil
}

func (r *repositoryStub) FinalizeAttempt(
	_ context.Context,
	_ FinalizeAttemptInput,
	_ time.Time,
) error {
	return nil
}

func TestNewDelivery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	input := InputSubmit{
		URL:     "https://example.com/webhooks",
		Payload: `{"event":"delivery.created"}`,
		Headers: map[string]string{"X-Request-ID": "request-1"},
	}

	got := newDelivery(input, "delivery-1", now)

	if got.ID != "delivery-1" || got.URL != input.URL || got.Payload != input.Payload {
		t.Errorf("delivery identity = %#v, want input values", got)
	}
	if got.Status != domain.DeliveryStatusPending || got.AttemptsMade != 0 || got.MaxAttempts != 8 {
		t.Errorf("delivery state = %#v, want pending with default retries", got)
	}
	if !got.NextAttemptAt.Equal(now) || !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Errorf("delivery times = %#v, want %s", got, now)
	}
	if !maps.Equal(got.Headers, input.Headers) {
		t.Errorf("headers = %#v, want %#v", got.Headers, input.Headers)
	}
	if got.IdempotencyKey != nil {
		t.Errorf("absent idempotency key = %v, want nil", got.IdempotencyKey)
	}

	input.Headers["X-Request-ID"] = "changed"
	if got.Headers["X-Request-ID"] != "request-1" {
		t.Errorf("headers were not cloned: %#v", got.Headers)
	}
}

func TestServiceSubmit(t *testing.T) {
	t.Parallel()

	t.Run("persists a pending delivery", func(t *testing.T) {
		repository := &repositoryStub{}
		service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), repository)

		delivery, err := service.Submit(context.Background(), InputSubmit{URL: "https://example.com"})
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		if _, err := uuid.Parse(delivery.ID); err != nil {
			t.Errorf("delivery ID = %q, want UUID: %v", delivery.ID, err)
		}
		if delivery.Status != domain.DeliveryStatusPending {
			t.Errorf("status = %q, want %q", delivery.Status, domain.DeliveryStatusPending)
		}
		if repository.created.ID != delivery.ID {
			t.Errorf("persisted ID = %q, want %q", repository.created.ID, delivery.ID)
		}
	})

	t.Run("returns repository error", func(t *testing.T) {
		wantErr := errors.New("database unavailable")
		service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), &repositoryStub{createErr: wantErr})

		delivery, err := service.Submit(context.Background(), InputSubmit{URL: "https://example.com"})
		if !errors.Is(err, wantErr) {
			t.Errorf("Submit() error = %v, want %v", err, wantErr)
		}
		if delivery != nil {
			t.Errorf("delivery = %#v, want nil", delivery)
		}
	})
}

func TestServiceGet(t *testing.T) {
	t.Parallel()

	wantDelivery := &domain.Delivery{ID: "delivery-1"}
	t.Run("returns repository delivery", func(t *testing.T) {
		service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), &repositoryStub{getDelivery: wantDelivery})

		got, err := service.Get(context.Background(), wantDelivery.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got != wantDelivery {
			t.Errorf("Get() = %#v, want %#v", got, wantDelivery)
		}
	})

	t.Run("returns repository error", func(t *testing.T) {
		wantErr := errors.New("delivery not found")
		service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), &repositoryStub{getErr: wantErr})

		got, err := service.Get(context.Background(), "missing")
		if !errors.Is(err, wantErr) {
			t.Errorf("Get() error = %v, want %v", err, wantErr)
		}
		if got != nil {
			t.Errorf("Get() = %#v, want nil", got)
		}
	})
}

func TestServiceSubmitIdempotency(t *testing.T) {
	t.Parallel()
	key := "order-123"
	existing := &domain.Delivery{ID: "original", URL: "https://original.example", IdempotencyKey: &key}
	lookupFailure := errors.New("lookup unavailable")
	cases := []struct {
		name        string
		lookup      func(context.Context, string) (*domain.Delivery, error)
		createErr   error
		wantErr     error
		wantCreates int
		wantOld     bool
	}{
		{
			name: "existing key ignores replacement content",
			lookup: func(context.Context, string) (*domain.Delivery, error) {
				return existing, nil
			},
			wantOld: true,
		},
		{name: "new key creates delivery", wantCreates: 1},
		{
			name: "lookup error does not insert",
			lookup: func(context.Context, string) (*domain.Delivery, error) {
				return nil, lookupFailure
			},
			wantErr: lookupFailure,
		},
		{
			name: "concurrent insert conflict returns winner",
			lookup: func() func(context.Context, string) (*domain.Delivery, error) {
				calls := 0
				return func(context.Context, string) (*domain.Delivery, error) {
					calls++
					if calls == 1 {
						return nil, fmt.Errorf("lookup: %w", sql.ErrNoRows)
					}
					return existing, nil
				}
			}(),
			createErr: fmt.Errorf("insert: %w", ErrIdempotencyKeyExists), wantCreates: 1, wantOld: true,
		},
		{
			name: "conflict lookup error propagates",
			lookup: func() func(context.Context, string) (*domain.Delivery, error) {
				calls := 0
				return func(context.Context, string) (*domain.Delivery, error) {
					calls++
					if calls == 1 {
						return nil, sql.ErrNoRows
					}
					return nil, lookupFailure
				}
			}(),
			createErr: ErrIdempotencyKeyExists, wantCreates: 1, wantErr: lookupFailure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repositoryStub{lookup: tc.lookup, createErr: tc.createErr}
			service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), repo)
			got, err := service.Submit(context.Background(), InputSubmit{
				URL: "https://replacement.example", Payload: "changed", IdempotencyKey: &key,
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Submit() error = %v, want %v", err, tc.wantErr)
			}
			if repo.createCalls != tc.wantCreates {
				t.Errorf("Create calls = %d, want %d", repo.createCalls, tc.wantCreates)
			}
			if tc.wantErr != nil {
				if got != nil {
					t.Errorf("failed submission returned %+v", got)
				}
				return
			}
			if tc.wantOld {
				if got != existing {
					t.Errorf("Submit returned %+v, want original %+v", got, existing)
				}
			} else if got == nil || got.IdempotencyKey == nil || *got.IdempotencyKey != key || got.ID != repo.created.ID {
				t.Errorf("new submission did not preserve key/ID: %+v", got)
			}
		})
	}
}
