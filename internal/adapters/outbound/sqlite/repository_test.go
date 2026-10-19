package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	applyTestMigration(t, databases[0], "003_add_attempt_response_body.up.sql")
	applyTestMigration(t, databases[0], "004_add_delivery_listing_indexes.up.sql")
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
	applyTestMigration(t, db, "003_add_attempt_response_body.up.sql")
	applyTestMigration(t, db, "004_add_delivery_listing_indexes.up.sql")
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

func TestRepositoryListAttempts(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, id := range []string{"delivery-1", "other", "empty"} {
		if err := repo.Create(ctx, testPendingDelivery(id, nil)); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Date(2026, 10, 10, 12, 0, 0, 123456789, time.UTC)
	finished := started.Add(1500 * time.Millisecond)
	status := 503
	message := "webhook request failed with status 503"
	outcome := domain.AttemptOutcomeFailed
	// Insert out of order to verify ordering comes from the query, not insertion.
	insertTestAttempt(t, db, "delivery-1", 2, formatTime(started), formatTime(finished), string(outcome), status, message, 1500)
	insertTestAttempt(t, db, "other", 1, formatTime(started), nil, nil, nil, nil, nil)
	insertTestAttempt(t, db, "delivery-1", 1, formatTime(started), nil, nil, nil, nil, nil)
	got, err := repo.ListAttempts(ctx, "delivery-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d attempts, want 2", len(got))
	}
	want := []*domain.Attempt{
		{ID: 1, DeliveryID: "delivery-1", AttemptNumber: 2, StartedAt: started, FinishedAt: &finished, Outcome: &outcome, ResponseStatus: &status, ErrorMessage: &message, Duration: 1500 * time.Millisecond},
		{ID: 3, DeliveryID: "delivery-1", AttemptNumber: 1, StartedAt: started},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attempts=%#v / %#v, want %#v / %#v", got[0], got[1], want[0], want[1])
	}
	for _, id := range []string{"empty", "missing"} {
		got, err := repo.ListAttempts(ctx, id)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("ListAttempts(%q)=%+v, %v; want non-nil empty slice", id, got, err)
		}
	}
}

func insertTestAttempt(t *testing.T, db *sql.DB, id string, number int, started string, finished, outcome, status, message, duration any) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO delivery_attempts
 (delivery_id, attempt_number, started_at, finished_at, outcome, response_status, error_message, duration_ms)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, number, started, finished, outcome, status, message, duration); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryListAttemptsErrors(t *testing.T) {
	for _, tc := range []struct {
		name, started string
		finished      any
		want          string
	}{
		{"invalid start time", "invalid", nil, "parse attempt 1 start time"},
		{"invalid finish time", "2026-10-10T12:00:00Z", "invalid", "parse attempt 1 finish time"},
		{"invalid duration", "2026-10-10T12:00:00Z", nil, "scan delivery attempt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := repo.Create(ctx, testPendingDelivery("delivery-1", nil)); err != nil {
				t.Fatal(err)
			}
			var duration any
			if tc.name == "invalid duration" {
				duration = "not-a-number"
			}
			insertTestAttempt(t, db, "delivery-1", 1, tc.started, tc.finished, nil, nil, nil, duration)
			got, err := repo.ListAttempts(ctx, "delivery-1")
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("attempts=%+v, error=%v; want %q", got, err, tc.want)
			}
			// With only one connection, this fails if the error path leaves rows open.
			if err := db.PingContext(ctx); err != nil {
				t.Fatalf("connection not released after mapping error: %v", err)
			}
		})
	}
	t.Run("canceled context", func(t *testing.T) {
		repo := NewRepository(openTestDB(t), slog.New(slog.NewTextHandler(io.Discard, nil)))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := repo.ListAttempts(ctx, "delivery-1")
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("attempts=%+v, error=%v", got, err)
		}
	})
	t.Run("query failure", func(t *testing.T) {
		db := openTestDB(t)
		repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if _, err := db.Exec("DROP TABLE delivery_attempts"); err != nil {
			t.Fatal(err)
		}
		got, err := repo.ListAttempts(context.Background(), "delivery-1")
		if got != nil || err == nil {
			t.Fatalf("attempts=%+v, error=%v", got, err)
		}
	})
}

func TestRepositoryFinalizeAttemptHistory(t *testing.T) {
	for _, tc := range []struct {
		name        string
		retry       bool
		maxAttempts int
		want        domain.DeliveryStatus
	}{
		{"permanent failure", false, 8, domain.DeliveryStatusDeadLetter},
		{"retryable failure", true, 8, domain.DeliveryStatusPending},
		{"exhausted attempts", true, 1, domain.DeliveryStatusDeadLetter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx := context.Background()
			delivery := testPendingDelivery("delivery-1", nil)
			delivery.MaxAttempts = tc.maxAttempts
			if err := repo.Create(ctx, delivery); err != nil {
				t.Fatal(err)
			}
			claim, err := repo.ClaimNext(ctx, delivery.CreatedAt, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			started := delivery.CreatedAt
			finished := started.Add(1500 * time.Millisecond)
			status := 404
			if tc.retry {
				status = 503
			}
			message := fmt.Sprintf("webhook request failed with status %d", status)
			input := application.FinalizeAttemptInput{
				DeliveryID: delivery.ID, LeaseToken: *claim.LeaseToken, ErrorMessage: &message,
				Result: application.SendResult{StartedAt: started, FinishedAt: finished, Duration: 1500 * time.Millisecond, Outcome: domain.AttemptOutcomeFailed, ResponseStatus: &status},
			}
			if tc.retry {
				retryAt := finished.Add(time.Minute)
				input.RetryAt = &retryAt
			}
			if err := repo.FinalizeAttempt(ctx, input, finished); err != nil {
				t.Fatal(err)
			}
			got, err := repo.Get(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want || got.AttemptsMade != 1 || got.LastError == nil || *got.LastError != message || got.LeaseToken != nil || got.LeaseExpiresAt != nil {
				t.Fatalf("incorrect finalized delivery: %+v", got)
			}
			if tc.want == domain.DeliveryStatusPending && !got.NextAttemptAt.Equal(*input.RetryAt) {
				t.Fatalf("incorrect retry time: %v", got.NextAttemptAt)
			}
			attempts, err := repo.ListAttempts(ctx, delivery.ID)
			if err != nil || len(attempts) != 1 {
				t.Fatalf("attempts=%+v, error=%v", attempts, err)
			}
			attempt := attempts[0]
			if attempt.AttemptNumber != 1 || attempt.ErrorMessage == nil || *attempt.ErrorMessage != message || attempt.ResponseStatus == nil || *attempt.ResponseStatus != status || attempt.Duration != input.Result.Duration || attempt.Outcome == nil || *attempt.Outcome != input.Result.Outcome {
				t.Fatalf("incorrect attempt: %+v", attempt)
			}
			// Repeated finalization must not increment the counter or add an attempt.
			if err := repo.FinalizeAttempt(ctx, input, finished); !errors.Is(err, application.ErrLeaseLost) {
				t.Fatalf("duplicate finalization error=%v", err)
			}
			attempts, err = repo.ListAttempts(ctx, delivery.ID)
			if err != nil || len(attempts) != 1 {
				t.Fatalf("duplicate attempt persisted: %+v, %v", attempts, err)
			}
			if tc.want != domain.DeliveryStatusPending {
				return
			}
			// A successful retry clears last_error while preserving the failed history.
			claim, err = repo.ClaimNext(ctx, *input.RetryAt, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			status = 204
			input = application.FinalizeAttemptInput{DeliveryID: delivery.ID, LeaseToken: *claim.LeaseToken,
				Result: application.SendResult{StartedAt: *input.RetryAt, FinishedAt: input.RetryAt.Add(time.Second), Duration: time.Second, Outcome: domain.AttemptOutcomeSucceeded, ResponseStatus: &status}}
			if err := repo.FinalizeAttempt(ctx, input, input.Result.FinishedAt); err != nil {
				t.Fatal(err)
			}
			got, err = repo.Get(ctx, delivery.ID)
			if err != nil || got.Status != domain.DeliveryStatusDelivered || got.AttemptsMade != 2 || got.LastError != nil || got.DeliveredAt == nil || !got.DeliveredAt.Equal(input.Result.FinishedAt) {
				t.Fatalf("successful retry=%+v, %v", got, err)
			}
			attempts, err = repo.ListAttempts(ctx, delivery.ID)
			if err != nil || len(attempts) != 2 {
				t.Fatalf("history=%+v, %v", attempts, err)
			}
			if attempts[0].AttemptNumber != 2 || attempts[0].ErrorMessage != nil || attempts[1].ErrorMessage == nil || *attempts[1].ErrorMessage != message {
				t.Fatalf("history not preserved: %+v / %+v", attempts[0], attempts[1])
			}
		})
	}
}

func TestRepositoryFinalizationOwnershipAndRollback(t *testing.T) {
	for _, mode := range []string{"reclaimed lease", "attempt insert failure"} {
		t.Run(mode, func(t *testing.T) {
			db := openTestDB(t)
			repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx := context.Background()
			delivery := testPendingDelivery("delivery-1", nil)
			if err := repo.Create(ctx, delivery); err != nil {
				t.Fatal(err)
			}
			original, err := repo.ClaimNext(ctx, delivery.CreatedAt, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			finished := delivery.CreatedAt.Add(2 * time.Minute)
			status := 204
			input := application.FinalizeAttemptInput{DeliveryID: delivery.ID, LeaseToken: *original.LeaseToken,
				Result: application.SendResult{StartedAt: delivery.CreatedAt, FinishedAt: finished, Duration: 2 * time.Minute, Outcome: domain.AttemptOutcomeSucceeded, ResponseStatus: &status}}
			if mode == "reclaimed lease" {
				replacement, err := repo.ClaimNext(ctx, finished, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if *replacement.LeaseToken == *original.LeaseToken {
					t.Fatal("reclaim reused lease token")
				}
			} else {
				if _, err := db.Exec(`CREATE TRIGGER reject_attempt BEFORE INSERT ON delivery_attempts BEGIN SELECT RAISE(ABORT, 'attempt insert failed'); END;`); err != nil {
					t.Fatal(err)
				}
			}
			before, err := repo.Get(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			err = repo.FinalizeAttempt(ctx, input, finished)
			if err == nil {
				t.Fatal("expected finalization failure")
			}
			if mode == "reclaimed lease" && !errors.Is(err, application.ErrLeaseLost) {
				t.Fatalf("error=%v, want lease lost", err)
			}
			after, getErr := repo.Get(ctx, delivery.ID)
			if getErr != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("failed finalization modified delivery: before=%+v after=%+v error=%v", before, after, getErr)
			}
			attempts, listErr := repo.ListAttempts(ctx, delivery.ID)
			if listErr != nil || len(attempts) != 0 {
				t.Fatalf("failed finalization persisted attempt: %+v, %v", attempts, listErr)
			}
		})
	}
}

func TestAttemptResponseBodyMigration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	applyTestMigration(t, db, "001_init.up.sql")
	applyTestMigration(t, db, "002_normalize_timestamps.up.sql")
	repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	delivery := testPendingDelivery("delivery-1", nil)
	if err := repo.Create(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	insertTestAttempt(t, db, delivery.ID, 1, formatTime(delivery.CreatedAt), nil, nil, nil, nil, nil)
	applyTestMigration(t, db, "003_add_attempt_response_body.up.sql")
	var body sql.NullString
	var truncated int
	if err := db.QueryRow("SELECT response_body, response_body_truncated FROM delivery_attempts WHERE id = 1").Scan(&body, &truncated); err != nil {
		t.Fatal(err)
	}
	if body.Valid || truncated != 0 {
		t.Fatalf("existing row defaults: body=%+v truncated=%d", body, truncated)
	}
	response := `{"error":"missing order_id"}`
	if _, err := db.Exec("UPDATE delivery_attempts SET response_body = ?, response_body_truncated = 1 WHERE id = 1", response); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT response_body, response_body_truncated FROM delivery_attempts WHERE id = 1").Scan(&body, &truncated); err != nil {
		t.Fatal(err)
	}
	if !body.Valid || body.String != response || truncated != 1 {
		t.Fatalf("response round-trip: body=%+v truncated=%d", body, truncated)
	}
	for _, value := range []any{-1, 2, nil} {
		if _, err := db.Exec("UPDATE delivery_attempts SET response_body_truncated = ? WHERE id = 1", value); err == nil {
			t.Fatalf("accepted invalid truncation flag %v", value)
		}
	}
	applyTestMigration(t, db, "003_add_attempt_response_body.down.sql")
	var count int
	if err := db.QueryRow("SELECT count(*) FROM delivery_attempts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("rollback lost attempt: count=%d error=%v", count, err)
	}
	if _, err := db.Exec("SELECT response_body, response_body_truncated FROM delivery_attempts"); err == nil {
		t.Fatal("rollback did not remove response columns")
	}
	// The schema remains upgradeable after rollback.
	applyTestMigration(t, db, "003_add_attempt_response_body.up.sql")
}

func TestListDeliveriesPagination(t *testing.T) {
	db := openTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := NewRepository(db, logger)
	service := application.NewService(logger, repo)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	base := time.Date(2026, 10, 19, 8, 0, 0, 123456789, time.UTC)
	for _, item := range []struct {
		id     string
		offset time.Duration
		status domain.DeliveryStatus
	}{
		{"b", 0, domain.DeliveryStatusPending},
		{"e", time.Second, domain.DeliveryStatusDelivered},
		{"a", -time.Second, domain.DeliveryStatusPending},
		{"f", 0, domain.DeliveryStatusPending},
		{"d", 0, domain.DeliveryStatusPending},
	} {
		delivery := testPendingDelivery(item.id, nil)
		delivery.CreatedAt = base.Add(item.offset)
		delivery.UpdatedAt = delivery.CreatedAt
		delivery.Status = item.status
		if err := repo.Create(ctx, delivery); err != nil {
			t.Fatal(err)
		}
	}
	pending, delivered, dead := domain.DeliveryStatusPending, domain.DeliveryStatusDelivered, domain.DeliveryStatusDeadLetter
	for _, tc := range []struct {
		name   string
		status *domain.DeliveryStatus
		limit  uint
		want   []string
		pages  int
	}{
		{name: "multiple pages with timestamp ties", limit: 2, want: []string{"e", "f", "d", "b", "a"}, pages: 3},
		{name: "single item pages", limit: 1, want: []string{"e", "f", "d", "b", "a"}, pages: 5},
		{name: "exact full final page with filter", status: &pending, limit: 2, want: []string{"f", "d", "b", "a"}, pages: 2},
		{name: "single result", status: &delivered, limit: 2, want: []string{"e"}, pages: 1},
		{name: "empty filter", status: &dead, limit: 2, want: []string{}, pages: 1},
		{name: "all in one page", limit: 100, want: []string{"e", "f", "d", "b", "a"}, pages: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids := []string{}
			query := application.ListDeliveriesQuery{Status: tc.status, Limit: tc.limit}
			pages := 0
			for {
				pages++
				if pages > len(tc.want)+1 {
					t.Fatal("pagination did not terminate")
				}
				page, err := service.ListDeliveries(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				if page.Deliveries == nil || len(page.Deliveries) > int(tc.limit) {
					t.Fatalf("invalid page: %+v", page)
				}
				for _, delivery := range page.Deliveries {
					ids = append(ids, delivery.ID)
					if tc.status != nil && delivery.Status != *tc.status {
						t.Fatalf("wrong filtered status: %s", delivery.Status)
					}
				}
				if page.NextCursor == "" {
					break
				}
				if len(page.Deliveries) != int(tc.limit) {
					t.Fatal("cursor on incomplete page")
				}
				data, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
				if err != nil {
					t.Fatal(err)
				}
				var cursor application.Cursor
				if err := json.Unmarshal(data, &cursor); err != nil {
					t.Fatal(err)
				}
				last := page.Deliveries[len(page.Deliveries)-1]
				if cursor.ID == nil || *cursor.ID != last.ID || cursor.CreatedAt == nil || *cursor.CreatedAt != formatTime(last.CreatedAt) || !reflect.DeepEqual(cursor.Status, tc.status) {
					t.Fatalf("cursor is not last returned row: %+v", cursor)
				}
				next := page.NextCursor
				query.CursorRaw = &next
			}
			if !reflect.DeepEqual(ids, tc.want) || pages != tc.pages {
				t.Fatalf("IDs=%v pages=%d, want %v / %d", ids, pages, tc.want, tc.pages)
			}
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		got, err := service.ListDeliveries(canceled, application.ListDeliveriesQuery{Limit: 2})
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("page=%+v error=%v", got, err)
		}
	})
}

func TestListDeliveriesEmptyDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := application.NewService(logger, NewRepository(openTestDB(t), logger))
	page, err := service.ListDeliveries(context.Background(), application.ListDeliveriesQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Deliveries == nil || len(page.Deliveries) != 0 || page.NextCursor != "" {
		t.Fatalf("invalid empty page: %+v", page)
	}
}

func TestMigrateFreshDatabaseAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.db")
	open := func() *sql.DB {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	db := open()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	delivery := testPendingDelivery("survives-restart", nil)
	if err := repo.Create(context.Background(), delivery); err != nil {
		t.Fatalf("database unusable after migration: %v", err)
	}
	insertTestAttempt(t, db, delivery.ID, 1, formatTime(delivery.CreatedAt), nil, nil, nil, nil, nil)
	// No pending migration is a successful no-op.
	if err := Migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = open()
	if err := Migrate(db); err != nil {
		t.Fatalf("restart migration: %v", err)
	}
	repo = NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got, err := repo.Get(context.Background(), delivery.ID)
	if err != nil || !reflect.DeepEqual(got, &delivery) {
		t.Fatalf("delivery lost on restart: %+v, %v", got, err)
	}
	attempts, err := repo.ListAttempts(context.Background(), delivery.ID)
	if err != nil || len(attempts) != 1 || attempts[0].ResponseBody != nil || attempts[0].ResponseBodyTruncated {
		t.Fatalf("attempt history lost on restart: %+v, %v", attempts, err)
	}
	var version int
	var dirty bool
	if err := db.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 4 || dirty {
		t.Fatalf("version=%d dirty=%t error=%v", version, dirty, err)
	}
	var indexes int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('deliveries_created_id_idx', 'deliveries_status_created_id_idx')").Scan(&indexes); err != nil || indexes != 2 {
		t.Fatalf("listing indexes=%d error=%v", indexes, err)
	}
}

func TestMigrateExistingCLIDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dispatch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"001_init.up.sql", "002_normalize_timestamps.up.sql", "003_add_attempt_response_body.up.sql"} {
		applyTestMigration(t, db, name)
	}
	// Match the version table used by the golang-migrate CLI.
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version uint64, dirty bool);
 CREATE UNIQUE INDEX version_unique ON schema_migrations(version);
 INSERT INTO schema_migrations VALUES (3, false);`); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	delivery := testPendingDelivery("existing-delivery", nil)
	if err := repo.Create(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("upgrade from CLI-managed schema: %v", err)
	}
	got, err := repo.Get(context.Background(), delivery.ID)
	if err != nil || !reflect.DeepEqual(got, &delivery) {
		t.Fatalf("upgrade changed delivery: %+v, %v", got, err)
	}
	var version int
	if err := db.QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil || version != 4 {
		t.Fatalf("version=%d error=%v", version, err)
	}
}

func TestMigrateRejectsUnusableSchema(t *testing.T) {
	for _, mode := range []string{"dirty schema", "newer schema", "migration failure"} {
		t.Run(mode, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			if mode == "migration failure" {
				if _, err := db.Exec("CREATE TABLE deliveries (id TEXT PRIMARY KEY); INSERT INTO deliveries VALUES ('preserve-me')"); err != nil {
					t.Fatal(err)
				}
			} else {
				version, dirty := 3, true
				if mode == "newer schema" {
					version, dirty = 99, false
				}
				if _, err := db.Exec("CREATE TABLE schema_migrations (version uint64, dirty bool)"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("INSERT INTO schema_migrations VALUES (?, ?)", version, dirty); err != nil {
					t.Fatal(err)
				}
			}
			if err := Migrate(db); err == nil {
				t.Fatal("expected startup migration failure")
			}
			if err := db.Ping(); err != nil {
				t.Fatalf("migration closed caller database: %v", err)
			}
			if mode == "migration failure" {
				var id string
				if err := db.QueryRow("SELECT id FROM deliveries").Scan(&id); err != nil || id != "preserve-me" {
					t.Fatalf("existing data changed: %q, %v", id, err)
				}
				var dirty bool
				if err := db.QueryRow("SELECT dirty FROM schema_migrations").Scan(&dirty); err != nil || !dirty {
					t.Fatalf("failed migration not marked dirty: %t, %v", dirty, err)
				}
				if err := Migrate(db); err == nil {
					t.Fatal("retry should reject dirty migration")
				}
			}
		})
	}
}
