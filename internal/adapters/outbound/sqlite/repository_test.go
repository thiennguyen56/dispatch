package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
	_ "modernc.org/sqlite"
)

func TestRepositoryGetRoundTripsHeaders(t *testing.T) {
	db := openTestDB(t)
	repository := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	leaseToken := "lease-1"
	lastError := "temporary failure"
	idempotencyKey := "request-1"
	leaseExpiresAt := now.Add(time.Minute)
	deliveredAt := now.Add(2 * time.Minute)
	delivery := domain.Delivery{
		ID:             "delivery-1",
		URL:            "https://example.com/webhooks",
		Payload:        `{"event":"delivery.created"}`,
		Headers:        map[string]string{"Authorization": "Bearer <REDACTED>"},
		Status:         domain.DeliveryStatusDelivered,
		AttemptsMade:   2,
		MaxAttempts:    8,
		NextAttemptAt:  now,
		LeaseToken:     &leaseToken,
		LeaseExpiresAt: &leaseExpiresAt,
		LastError:      &lastError,
		IdempotencyKey: &idempotencyKey,
		CreatedAt:      now,
		UpdatedAt:      now,
		DeliveredAt:    &deliveredAt,
	}

	if err := repository.Create(context.Background(), delivery); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repository.Get(context.Background(), delivery.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !reflect.DeepEqual(got, &delivery) {
		t.Errorf("Get() = %#v, want %#v", got, &delivery)
	}

	_, err = repository.Get(context.Background(), "missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("Get(missing) error = %v, want %v", err, sql.ErrNoRows)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migration, err := os.ReadFile("migration/001_init.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	return db
}
