package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thiennguyen56/dispatch/internal/application"
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

func testPendingDelivery(id string, key *string) domain.Delivery {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	return domain.Delivery{
		ID: id, URL: "https://example.com", Payload: "original",
		Headers: map[string]string{"Content-Type": "text/plain"},
		Status:  domain.DeliveryStatusPending, MaxAttempts: 8,
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now, IdempotencyKey: key,
	}
}

func TestRepositoryCreateIdempotency(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	key := "order-123"
	original := testPendingDelivery("original", &key)
	if err := repo.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	duplicate := testPendingDelivery("duplicate", &key)
	duplicate.URL = "https://replacement.example"
	duplicate.Payload = "replacement"
	if err := repo.Create(ctx, duplicate); !errors.Is(err, application.ErrIdempotencyKeyExists) {
		t.Fatalf("duplicate Create() error = %v", err)
	}
	got, err := repo.GetByIdempotencyKey(ctx, key)
	if err != nil || !reflect.DeepEqual(got, &original) {
		t.Fatalf("original delivery changed: %+v, %v", got, err)
	}
	if _, err := repo.Get(ctx, duplicate.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("duplicate ID was persisted: %v", err)
	}
	if _, err := repo.GetByIdempotencyKey(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing-key lookup error = %v", err)
	}

	for _, id := range []string{"unkeyed-1", "unkeyed-2"} {
		if err := repo.Create(ctx, testPendingDelivery(id, nil)); err != nil {
			t.Fatalf("unkeyed Create(): %v", err)
		}
		got, err := repo.Get(ctx, id)
		if err != nil || got.IdempotencyKey != nil {
			t.Fatalf("unkeyed delivery = %+v, %v", got, err)
		}
	}
}

func TestRepositoryCreateReturnsUnrelatedErrors(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	if err := repo.Create(ctx, testPendingDelivery("existing", nil)); err != nil {
		t.Fatal(err)
	}
	invalid := testPendingDelivery("invalid", nil)
	invalid.MaxAttempts = 0
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		delivery domain.Delivery
	}{
		{"primary-key conflict", ctx, testPendingDelivery("existing", nil)},
		{"check constraint", ctx, invalid},
		{"canceled context", canceled, testPendingDelivery("canceled", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := repo.Create(tc.ctx, tc.delivery)
			if err == nil || errors.Is(err, application.ErrIdempotencyKeyExists) {
				t.Fatalf("Create error = %v, want unrelated error", err)
			}
			if tc.ctx == canceled && !errors.Is(err, context.Canceled) {
				t.Errorf("cancellation was not preserved: %v", err)
			}
		})
	}
}

// Force both initial service lookups to finish before either inserts. This
// exercises the conflict fallback using separate file-backed connections.
type synchronizedLookupRepository struct {
	*Repository
	lookups *atomic.Int32
	ready   chan struct{}
}

func (r synchronizedLookupRepository) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Delivery, error) {
	delivery, err := r.Repository.GetByIdempotencyKey(ctx, key)
	count := r.lookups.Add(1)
	if count <= 2 {
		if count == 2 {
			close(r.ready)
		}
		select {
		case <-r.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return delivery, err
}

func TestServiceConcurrentSubmissionsReturnSameDelivery(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "dispatch.db") + "?_pragma=busy_timeout(3000)"
	var databases [2]*sql.DB
	for i := range databases {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		databases[i] = db
		t.Cleanup(func() { _ = db.Close() })
	}
	applyTestMigration(t, databases[0], "001_init.up.sql")
	applyTestMigration(t, databases[0], "002_normalize_timestamps.up.sql")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var lookups atomic.Int32
	ready := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := "concurrent-request"
	type result struct {
		delivery *domain.Delivery
		err      error
	}
	results := make(chan result, 2)
	for _, db := range databases {
		repo := synchronizedLookupRepository{NewRepository(db, log), &lookups, ready}
		service := application.NewService(log, repo)
		go func() {
			delivery, err := service.Submit(ctx, application.InputSubmit{
				URL: "https://example.com", Payload: "original", IdempotencyKey: &key,
			})
			results <- result{delivery, err}
		}()
	}
	var winner *domain.Delivery
	for range 2 {
		var got result
		select {
		case got = <-results:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if got.err != nil || got.delivery == nil {
			t.Fatalf("concurrent submission = %+v, %v", got.delivery, got.err)
		}
		if winner == nil {
			winner = got.delivery
		} else if winner.ID != got.delivery.ID || winner.URL != got.delivery.URL ||
			winner.Payload != got.delivery.Payload || !winner.CreatedAt.Equal(got.delivery.CreatedAt) {
			t.Fatalf("submissions returned different deliveries: %+v / %+v", winner, got.delivery)
		}
	}
	var count int
	if err := databases[0].QueryRow("SELECT count(*) FROM deliveries").Scan(&count); err != nil || count != 1 {
		t.Fatalf("persisted count = %d, error = %v; want 1", count, err)
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
