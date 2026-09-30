package application

import (
	"context"
	"errors"
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
}

func (r *repositoryStub) Create(_ context.Context, delivery domain.Delivery) error {
	r.created = delivery
	return r.createErr
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
