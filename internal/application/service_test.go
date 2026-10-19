package application

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type repositoryStub struct {
	listDeliveries func(context.Context, ListDeliveriesQuery) (*DeliveryPage, error)
	claim          func(context.Context, time.Time, time.Duration) (*domain.Delivery, error)
	finalize       func(context.Context, FinalizeAttemptInput, time.Time) error
	listAttempts   func(context.Context, string) ([]*domain.Attempt, error)
	created        domain.Delivery
	createErr      error
	getDelivery    *domain.Delivery
	getErr         error
	createCalls    int
	lookup         func(context.Context, string) (*domain.Delivery, error)
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

func (r *repositoryStub) ListDeliveries(ctx context.Context, query ListDeliveriesQuery) (*DeliveryPage, error) {
	if r.listDeliveries != nil {
		return r.listDeliveries(ctx, query)
	}
	return nil, nil
}

func (r *repositoryStub) ListAttempts(ctx context.Context, id string) ([]*domain.Attempt, error) {
	if r.listAttempts != nil {
		return r.listAttempts(ctx, id)
	}
	return []*domain.Attempt{}, nil
}

func (r *repositoryStub) ClaimNext(ctx context.Context, now time.Time, lease time.Duration) (*domain.Delivery, error) {
	if r.claim != nil {
		return r.claim(ctx, now, lease)
	}
	return nil, nil
}

func (r *repositoryStub) FinalizeAttempt(ctx context.Context, input FinalizeAttemptInput, now time.Time) error {
	if r.finalize != nil {
		return r.finalize(ctx, input, now)
	}
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

func TestServiceListAttempts(t *testing.T) {
	t.Parallel()
	failure := errors.New("database unavailable")
	for _, tc := range []struct {
		name     string
		attempts []*domain.Attempt
		err      error
	}{
		{name: "returns history", attempts: []*domain.Attempt{{ID: 2, DeliveryID: "delivery-1", AttemptNumber: 2}, {ID: 1, DeliveryID: "delivery-1", AttemptNumber: 1}}},
		{name: "returns empty history", attempts: []*domain.Attempt{}},
		{name: "propagates repository error", err: failure},
		{name: "preserves cancellation", err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if errors.Is(tc.err, context.Canceled) {
				cancel()
			}
			calls := 0
			repo := &repositoryStub{listAttempts: func(gotCtx context.Context, id string) ([]*domain.Attempt, error) {
				calls++
				if gotCtx != ctx || id != "delivery-1" {
					t.Errorf("unexpected context or ID: %v / %q", gotCtx, id)
				}
				return tc.attempts, tc.err
			}}
			service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), repo)
			got, err := service.ListAttempts(ctx, "delivery-1")
			if calls != 1 || !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.attempts) {
				t.Fatalf("calls=%d, attempts=%+v, error=%v", calls, got, err)
			}
		})
	}
}

type senderFunc func(context.Context, domain.Delivery) (SendResult, error)

func (f senderFunc) Send(ctx context.Context, d domain.Delivery) (SendResult, error) {
	return f(ctx, d)
}

func TestProcessorFinalizesOutcomes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		status          int
		outcome         domain.AttemptOutcome
		sendErr         error
		retry           bool
		message         string
		cancelAfterSend bool
	}{
		{name: "success", status: 204, outcome: domain.AttemptOutcomeSucceeded},
		{name: "404 without Go error", status: 404, outcome: domain.AttemptOutcomeFailed, message: "webhook request failed with status 404"},
		{name: "408 without Go error", status: 408, outcome: domain.AttemptOutcomeFailed, retry: true, message: "webhook request failed with status 408"},
		{name: "429 without Go error", status: 429, outcome: domain.AttemptOutcomeFailed, retry: true, message: "webhook request failed with status 429"},
		{name: "503 without Go error", status: 503, outcome: domain.AttemptOutcomeFailed, retry: true, message: "webhook request failed with status 503"},
		{name: "transport failure", outcome: domain.AttemptOutcomeFailed, sendErr: errors.New("private transport details"), retry: true, message: "webhook transport failed"},
		{name: "timeout", outcome: domain.AttemptOutcomeTimedOut, sendErr: context.DeadlineExceeded, retry: true, message: "webhook request timed out"},
		{name: "success during shutdown", status: 204, outcome: domain.AttemptOutcomeSucceeded, cancelAfterSend: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			token := "lease-1"
			claim := &domain.Delivery{ID: "delivery-1", LeaseToken: &token}
			started := time.Now().Add(-time.Second)
			result := SendResult{StartedAt: started, FinishedAt: started.Add(time.Millisecond), Duration: time.Millisecond, Outcome: tc.outcome}
			if tc.status != 0 {
				result.ResponseStatus = &tc.status
			}
			claims, sends, finalizations := 0, 0, 0
			repo := &repositoryStub{
				claim: func(gotCtx context.Context, now time.Time, lease time.Duration) (*domain.Delivery, error) {
					claims++
					if gotCtx != ctx || now.IsZero() || lease <= 0 {
						t.Error("invalid claim arguments")
					}
					return claim, nil
				},
				finalize: func(finalCtx context.Context, input FinalizeAttemptInput, now time.Time) error {
					finalizations++
					if finalCtx.Err() != nil {
						t.Errorf("finalization context canceled: %v", finalCtx.Err())
					}
					if deadline, ok := finalCtx.Deadline(); !ok || deadline.After(time.Now().Add(finalizeTimeout)) {
						t.Error("finalization must have bounded deadline")
					}
					if input.DeliveryID != claim.ID || input.LeaseToken != token || !reflect.DeepEqual(input.Result, result) || now.Before(result.FinishedAt) {
						t.Errorf("incorrect finalization: %+v", input)
					}
					if tc.message == "" {
						if input.ErrorMessage != nil {
							t.Errorf("success has error: %v", input.ErrorMessage)
						}
					} else if input.ErrorMessage == nil || *input.ErrorMessage != tc.message {
						t.Errorf("error message=%v, want %q", input.ErrorMessage, tc.message)
					}
					if tc.retry {
						if input.RetryAt == nil || !input.RetryAt.Equal(result.FinishedAt.Add(time.Minute)) {
							t.Errorf("retry time=%v", input.RetryAt)
						}
					} else if input.RetryAt != nil {
						t.Errorf("unexpected retry=%v", input.RetryAt)
					}
					return nil
				},
			}
			sender := senderFunc(func(gotCtx context.Context, d domain.Delivery) (SendResult, error) {
				sends++
				if gotCtx != ctx || d.ID != claim.ID {
					t.Error("unexpected send arguments")
				}
				if tc.cancelAfterSend {
					cancel()
				}
				return result, tc.sendErr
			})
			processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), repo, sender)
			if err := processor.ProcessNext(ctx); err != nil {
				t.Fatal(err)
			}
			if claims != 1 || sends != 1 || finalizations != 1 {
				t.Fatalf("claims=%d sends=%d finalizations=%d", claims, sends, finalizations)
			}
		})
	}
}

func TestProcessorPropagatesRepositoryErrors(t *testing.T) {
	t.Parallel()
	claimFailure := errors.New("claim failed")
	for _, tc := range []struct {
		name                  string
		claimErr, finalizeErr error
	}{
		{name: "no job", claimErr: ErrNoJob{}},
		{name: "claim failure", claimErr: claimFailure},
		{name: "lease lost", finalizeErr: ErrLeaseLost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := "lease-1"
			sends, finalizations := 0, 0
			repo := &repositoryStub{
				claim: func(context.Context, time.Time, time.Duration) (*domain.Delivery, error) {
					return &domain.Delivery{ID: "delivery-1", LeaseToken: &token}, tc.claimErr
				},
				finalize: func(context.Context, FinalizeAttemptInput, time.Time) error { finalizations++; return tc.finalizeErr },
			}
			sender := senderFunc(func(context.Context, domain.Delivery) (SendResult, error) {
				sends++
				now := time.Now()
				status := 204
				return SendResult{StartedAt: now, FinishedAt: now, Outcome: domain.AttemptOutcomeSucceeded, ResponseStatus: &status}, nil
			})
			processor := NewProcessor(slog.New(slog.NewTextHandler(io.Discard, nil)), repo, sender)
			err := processor.ProcessNext(context.Background())
			want := tc.claimErr
			if want == nil {
				want = tc.finalizeErr
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v, want %v", err, want)
			}
			if tc.claimErr != nil && (sends != 0 || finalizations != 0) {
				t.Fatalf("processed unclaimed delivery: sends=%d finalizations=%d", sends, finalizations)
			}
			if tc.claimErr == nil && (sends != 1 || finalizations != 1) {
				t.Fatalf("sends=%d finalizations=%d", sends, finalizations)
			}
		})
	}
}

func TestServiceListDeliveriesInvalidCursor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw string }{
		{"invalid base64", "%%%"},
		{"invalid JSON", base64.RawURLEncoding.EncodeToString([]byte("{"))},
		{"wrong JSON type", base64.RawURLEncoding.EncodeToString([]byte("[]"))},
		{"null cursor", base64.RawURLEncoding.EncodeToString([]byte("null"))},
		{"empty object", base64.RawURLEncoding.EncodeToString([]byte(`{}`))},
		{"missing ID", base64.RawURLEncoding.EncodeToString([]byte(`{"CreatedAt":"2026-10-19T08:00:00Z"}`))},
		{"empty ID", base64.RawURLEncoding.EncodeToString([]byte(`{"ID":"  ","CreatedAt":"2026-10-19T08:00:00Z"}`))},
		{"missing creation time", base64.RawURLEncoding.EncodeToString([]byte(`{"ID":"delivery-1"}`))},
		{"invalid creation time", base64.RawURLEncoding.EncodeToString([]byte(`{"ID":"delivery-1","CreatedAt":"yesterday"}`))},
		{"zero creation time", base64.RawURLEncoding.EncodeToString([]byte(`{"ID":"delivery-1","CreatedAt":"0001-01-01T00:00:00Z"}`))},
		{"invalid status", base64.RawURLEncoding.EncodeToString([]byte(`{"ID":"delivery-1","CreatedAt":"2026-10-19T08:00:00Z","status":"unknown"}`))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repositoryStub{listDeliveries: func(context.Context, ListDeliveriesQuery) (*DeliveryPage, error) {
				t.Fatal("invalid cursor reached repository")
				return nil, nil
			}}
			service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), repo)
			page, err := service.ListDeliveries(context.Background(), ListDeliveriesQuery{Limit: 20, CursorRaw: &tc.raw})
			if page != nil || !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("page=%+v error=%v, want ErrInvalidCursor", page, err)
			}
		})
	}
}

func TestServiceListDeliveriesCursorForwarding(t *testing.T) {
	t.Parallel()
	failure := errors.New("database unavailable")
	for _, tc := range []struct {
		name    string
		raw     *string
		repoErr error
	}{
		{name: "first page"},
		{name: "valid cursor", raw: func() *string {
			s := base64.RawURLEncoding.EncodeToString([]byte(`{"ID":"delivery-1","CreatedAt":"2026-10-19T08:00:00.000000000Z","status":null}`))
			return &s
		}()},
		{name: "repository failure", repoErr: failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := &DeliveryPage{Deliveries: []*domain.Delivery{}}
			calls := 0
			repo := &repositoryStub{listDeliveries: func(_ context.Context, query ListDeliveriesQuery) (*DeliveryPage, error) {
				calls++
				if tc.raw != nil && (query.Cursor == nil || query.Cursor.ID == nil || *query.Cursor.ID != "delivery-1") {
					t.Errorf("cursor not decoded: %+v", query.Cursor)
				}
				if tc.repoErr != nil {
					return nil, tc.repoErr
				}
				return want, nil
			}}
			service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), repo)
			got, err := service.ListDeliveries(context.Background(), ListDeliveriesQuery{Limit: 20, CursorRaw: tc.raw})
			if calls != 1 || !errors.Is(err, tc.repoErr) || errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if tc.repoErr == nil && got != want {
				t.Fatalf("page=%+v, want %+v", got, want)
			}
		})
	}
}

func TestServiceListDeliveriesCursorFilterAndTime(t *testing.T) {
	t.Parallel()
	pending, delivered := domain.DeliveryStatusPending, domain.DeliveryStatusDelivered
	for _, tc := range []struct {
		name                      string
		cursorStatus, queryStatus *domain.DeliveryStatus
		invalid                   bool
	}{
		{name: "unfiltered"},
		{name: "same filter", cursorStatus: &pending, queryStatus: &pending},
		{name: "changed filter", cursorStatus: &pending, queryStatus: &delivered, invalid: true},
		{name: "removed filter", cursorStatus: &pending, invalid: true},
		{name: "added filter", queryStatus: &pending, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, createdAt := "delivery-1", "2026-10-19T10:00:00.123+02:00"
			data, err := json.Marshal(Cursor{ID: &id, CreatedAt: &createdAt, Status: tc.cursorStatus})
			if err != nil {
				t.Fatal(err)
			}
			raw := base64.RawURLEncoding.EncodeToString(data)
			calls := 0
			repo := &repositoryStub{listDeliveries: func(_ context.Context, query ListDeliveriesQuery) (*DeliveryPage, error) {
				calls++
				if query.Cursor == nil || query.Cursor.CreatedAt == nil || *query.Cursor.CreatedAt != "2026-10-19T08:00:00.123000000Z" {
					t.Errorf("timestamp not normalized: %+v", query.Cursor)
				}
				if !reflect.DeepEqual(query.Status, tc.queryStatus) {
					t.Errorf("query filter changed: %v", query.Status)
				}
				return &DeliveryPage{Deliveries: []*domain.Delivery{}}, nil
			}}
			service := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), repo)
			got, err := service.ListDeliveries(context.Background(), ListDeliveriesQuery{Limit: 2, Status: tc.queryStatus, CursorRaw: &raw})
			if tc.invalid {
				if !errors.Is(err, ErrInvalidCursor) || got != nil || calls != 0 {
					t.Fatalf("page=%+v error=%v calls=%d", got, err, calls)
				}
			} else if err != nil || got == nil || calls != 1 {
				t.Fatalf("page=%+v error=%v calls=%d", got, err, calls)
			}
		})
	}
}
