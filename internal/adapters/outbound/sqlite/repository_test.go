package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	db.SetMaxOpenConns(1)

	applyTestMigration(t, db, "001_init.up.sql")
	applyTestMigration(t, db, "002_normalize_timestamps.up.sql")
	return db
}

func applyTestMigration(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	migration, err := os.ReadFile("migration/" + name)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

func TestFormatTimeUsesFixedWidthUTC(t *testing.T) {
	input := time.Date(2026, 9, 30, 19, 0, 0, 500000000, time.FixedZone("UTC+7", 7*60*60))
	if got := formatTime(input); got != "2026-09-30T12:00:00.500000000Z" {
		t.Fatalf("formatTime() = %q", got)
	}
}

func TestTimestampMigrationPreservesInstantsAndNulls(t *testing.T) {
	db := openTestDB(t)
	legacyTimes := []string{
		"2026-09-30T12:00:00Z",
		"2026-09-30T12:00:00.1Z",
		"2026-09-30T12:00:00.12Z",
		"2026-09-30T12:00:00.123Z",
		"2026-09-30T12:00:00.1234Z",
		"2026-09-30T12:00:00.12345Z",
		"2026-09-30T12:00:00.123456Z",
		"2026-09-30T12:00:00.1234567Z",
		"2026-09-30T12:00:00.12345678Z",
		"2026-09-30T12:00:00.123456789Z",
		"2026-09-30T12:00:00.000000000Z",
	}
	for i, stamp := range legacyTimes {
		_, err := db.Exec(`INSERT INTO deliveries
			(id, target_url, payload, status, next_attempt_at, lease_expires_at,
			 created_at, updated_at, delivered_at)
			VALUES (?, 'https://example.com', '', 'pending', ?, ?, ?, ?, ?)`,
			fmt.Sprint(i), stamp, stamp, stamp, stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO delivery_attempts
			(delivery_id, attempt_number, started_at, finished_at) VALUES (?, 1, ?, ?)`,
			fmt.Sprint(i), stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE deliveries SET lease_expires_at = NULL, delivered_at = NULL WHERE id = '0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE delivery_attempts SET finished_at = NULL WHERE delivery_id = '0'`); err != nil {
		t.Fatal(err)
	}

	// Insert legacy representations above, then exercise upgrading and reapplying.
	for pass := 0; pass < 2; pass++ {
		applyTestMigration(t, db, "002_normalize_timestamps.up.sql")
		for i, stamp := range legacyTimes {
			original, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil {
				t.Fatal(err)
			}
			var values [7]sql.NullString
			err = db.QueryRow(`SELECT d.next_attempt_at, d.lease_expires_at,
				d.created_at, d.updated_at, d.delivered_at, a.started_at, a.finished_at
				FROM deliveries d JOIN delivery_attempts a ON a.delivery_id = d.id
				WHERE d.id = ?`, fmt.Sprint(i)).Scan(
				&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6])
			if err != nil {
				t.Fatal(err)
			}
			for column, value := range values {
				if i == 0 && (column == 1 || column == 4 || column == 6) {
					if value.Valid {
						t.Errorf("column %d: NULL became %q", column, value.String)
					}
					continue
				}
				parsed, err := parseTime(value.String)
				if err != nil || !parsed.Equal(original) || len(value.String) != 30 {
					t.Errorf("column %d: %q became %q (parse error: %v)", column, stamp, value.String, err)
				}
			}
		}
	}
	applyTestMigration(t, db, "002_normalize_timestamps.down.sql")
}

func TestClaimSQLTimestampBoundaries(t *testing.T) {
	base := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		due  time.Time
		now  time.Time
		want bool
	}{
		{"whole_second_due", base, base.Add(500 * time.Millisecond), true},
		{"fraction_still_future", base.Add(500 * time.Millisecond), base, false},
		{"equal", base.Add(100 * time.Millisecond), base.Add(100 * time.Millisecond), true},
		{"one_nanosecond_future", base.Add(100*time.Millisecond + time.Nanosecond), base.Add(100 * time.Millisecond), false},
	}
	for _, legacy := range []bool{false, true} {
		for _, status := range []domain.DeliveryStatus{domain.DeliveryStatusPending, domain.DeliveryStatusInProgress} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("legacy=%t/%s/%s", legacy, status, tc.name), func(t *testing.T) {
					db := openTestDB(t)
					repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
					oldToken := "old-token"
					delivery := domain.Delivery{
						ID: "delivery", URL: "https://example.com", Status: status,
						MaxAttempts: 8, NextAttemptAt: tc.due, LeaseExpiresAt: &tc.due,
						LeaseToken: &oldToken, CreatedAt: base, UpdatedAt: base,
					}
					if err := repo.Create(context.Background(), delivery); err != nil {
						t.Fatal(err)
					}
					if legacy {
						stamp := tc.due.Format(time.RFC3339Nano)
						if _, err := db.Exec(`UPDATE deliveries SET next_attempt_at = ?, lease_expires_at = ?`, stamp, stamp); err != nil {
							t.Fatal(err)
						}
						applyTestMigration(t, db, "002_normalize_timestamps.up.sql")
					}
					// Exercise SQL scheduling independently of ClaimNext's result mapping.
					var values [15]any
					var destinations []any
					for i := range values {
						destinations = append(destinations, &values[i])
					}
					err := db.QueryRow(claimNextDelivery,
						sql.Named("now", formatTime(tc.now)),
						sql.Named("lease_token", "new-token"),
						sql.Named("lease_expires_at", formatTime(tc.now.Add(time.Minute))),
					).Scan(destinations...)
					if !tc.want {
						if !errors.Is(err, sql.ErrNoRows) {
							t.Fatalf("future delivery/lease: got error %v, want sql.ErrNoRows", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if values[0] != delivery.ID || values[8] != "new-token" {
						t.Fatalf("unexpected claimed ID/token: %v / %v", values[0], values[8])
					}
				})
			}
		}
	}
}
