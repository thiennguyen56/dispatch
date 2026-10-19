package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type serviceStub struct {
	listDeliveries func(context.Context, application.ListDeliveriesQuery) (*application.DeliveryPage, error)
	listAttempts   func(context.Context, string) ([]*domain.Attempt, error)
	delivery       *domain.Delivery
	submitErr      error
	getErr         error
	submit         func(context.Context, application.InputSubmit) (*domain.Delivery, error)
}

func (s serviceStub) Submit(ctx context.Context, input application.InputSubmit) (*domain.Delivery, error) {
	if s.submit != nil {
		return s.submit(ctx, input)
	}
	return s.delivery, s.submitErr
}

func TestHandlerSubmitIdempotencyHeader(t *testing.T) {
	for _, key := range []string{"", "order-123"} {
		t.Run("key="+key, func(t *testing.T) {
			var captured application.InputSubmit
			handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
				submit: func(_ context.Context, input application.InputSubmit) (*domain.Delivery, error) {
					captured = input
					return &domain.Delivery{ID: "original"}, nil
				},
			})
			req := httptest.NewRequest(http.MethodPost, "/deliveries", bytes.NewBufferString(`{"url":"https://example.com"}`))
			if key != "" {
				req.Header.Set("Idempotency-Key", key)
			}
			response := httptest.NewRecorder()
			handler.Submit(response, req)
			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d", response.Code)
			}
			if key == "" {
				if captured.IdempotencyKey != nil {
					t.Errorf("absent header became key %v", captured.IdempotencyKey)
				}
			} else if captured.IdempotencyKey == nil || *captured.IdempotencyKey != key {
				t.Errorf("header was not forwarded: %v", captured.IdempotencyKey)
			}
			var body struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.ID != "original" {
				t.Errorf("original ID not returned: %+v, %v", body, err)
			}
		})
	}
}

func (s serviceStub) Get(context.Context, string) (*domain.Delivery, error) {
	return s.delivery, s.getErr
}

func TestHandlerSubmitServiceErrorReturnsStandardErrorResponse(t *testing.T) {
	t.Parallel()

	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
		submitErr: errors.New("database unavailable"),
	})
	req := httptest.NewRequest(http.MethodPost, "/deliveries", bytes.NewBufferString(`{"url":"https://example.com"}`))
	response := httptest.NewRecorder()

	handler.Submit(response, req)

	assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error", "database unavailable")
}

func TestHandlerGet(t *testing.T) {
	t.Parallel()

	t.Run("returns delivery", func(t *testing.T) {
		handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
			delivery: &domain.Delivery{ID: "delivery-1", Status: domain.DeliveryStatusPending},
		})
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/deliveries/delivery-1", nil), map[string]string{"id": "delivery-1"})
		response := httptest.NewRecorder()

		handler.Get(response, req)

		if got, want := response.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		var delivery domain.Delivery
		if err := json.NewDecoder(response.Body).Decode(&delivery); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got, want := delivery.ID, "delivery-1"; got != want {
			t.Errorf("ID = %q, want %q", got, want)
		}
	})

	t.Run("rejects missing ID", func(t *testing.T) {
		handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		response := httptest.NewRecorder()

		handler.Get(response, httptest.NewRequest(http.MethodGet, "/deliveries/", nil))

		assertErrorResponse(t, response, http.StatusBadRequest, "invalid_request", "id query parameter is required")
	})

	t.Run("returns service error", func(t *testing.T) {
		handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{getErr: errors.New("not found")})
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/deliveries/missing", nil), map[string]string{"id": "missing"})
		response := httptest.NewRecorder()

		handler.Get(response, req)

		assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error", "not found")
	})
}

func assertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode, wantMessage string) {
	t.Helper()

	if got := response.Code; got != wantStatus {
		t.Fatalf("status = %d, want %d", got, wantStatus)
	}
	var body errorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := body.Error.Code; got != wantCode {
		t.Errorf("error code = %q, want %q", got, wantCode)
	}
	if got := body.Error.Message; got != wantMessage {
		t.Errorf("error message = %q, want %q", got, wantMessage)
	}
}

func TestHandlerSubmitReturnsStandardErrorResponse(t *testing.T) {
	t.Parallel()

	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	tests := []struct {
		name        string
		body        string
		wantMessage string
	}{
		{
			name:        "malformed JSON",
			body:        `{`,
			wantMessage: "request body must be valid JSON",
		},
		{
			name:        "invalid request",
			body:        `{"url":"ftp://example.com"}`,
			wantMessage: "url scheme must be http or https",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/deliveries", bytes.NewBufferString(test.body))
			response := httptest.NewRecorder()

			handler.Submit(response, req)

			if got, want := response.Code, http.StatusBadRequest; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
			if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}

			var body errorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got, want := body.Error.Code, "invalid_request"; got != want {
				t.Errorf("error code = %q, want %q", got, want)
			}
			if got, want := body.Error.Message, test.wantMessage; got != want {
				t.Errorf("error message = %q, want %q", got, want)
			}
		})
	}
}

func TestHandlerSubmitSuccessUsesJSONResponse(t *testing.T) {
	t.Parallel()

	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
		delivery: &domain.Delivery{ID: "delivery-1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/deliveries", bytes.NewBufferString(`{"url":"https://example.com"}`))
	response := httptest.NewRecorder()

	handler.Submit(response, req)

	if got, want := response.Code, http.StatusCreated; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, want := body["message"], "Submitted deliveries"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if got, want := body["id"], "delivery-1"; got != want {
		t.Errorf("id = %q, want %q", got, want)
	}
}

func (s serviceStub) ListAttempts(ctx context.Context, id string) ([]*domain.Attempt, error) {
	if s.listAttempts != nil {
		return s.listAttempts(ctx, id)
	}
	return []*domain.Attempt{}, nil
}

func TestHandlerListAttempts(t *testing.T) {
	t.Parallel()
	t.Run("returns attempts and forwards request context and ID", func(t *testing.T) {
		want := []*domain.Attempt{{ID: 2, DeliveryID: "delivery-1", AttemptNumber: 2, StartedAt: time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), Duration: 1500 * time.Millisecond}}
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/deliveries/delivery-1/attempts", nil), map[string]string{"id": "delivery-1"})
		calls := 0
		handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
			listAttempts: func(ctx context.Context, id string) ([]*domain.Attempt, error) {
				calls++
				if ctx != req.Context() || id != "delivery-1" {
					t.Errorf("unexpected context or ID: %v / %q", ctx, id)
				}
				return want, nil
			},
		})
		response := httptest.NewRecorder()
		handler.ListAttempts(response, req)
		if response.Code != http.StatusOK || calls != 1 {
			t.Fatalf("status=%d, calls=%d", response.Code, calls)
		}
		if response.Header().Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content type: %s", response.Header().Get("Content-Type"))
		}
		var got []*domain.Attempt
		if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("attempts=%+v, want %+v", got, want)
		}
	})
	t.Run("empty attempts serialize as array", func(t *testing.T) {
		handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{})
		response := httptest.NewRecorder()
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/deliveries/empty/attempts", nil), map[string]string{"id": "empty"})
		handler.ListAttempts(response, req)
		if response.Code != http.StatusOK || string(bytes.TrimSpace(response.Body.Bytes())) != "[]" {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
	})
	for _, id := range []string{"", "   "} {
		t.Run("rejects ID="+id, func(t *testing.T) {
			handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			response := httptest.NewRecorder()
			req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/deliveries/invalid/attempts", nil), map[string]string{"id": id})
			handler.ListAttempts(response, req)
			assertErrorResponse(t, response, http.StatusBadRequest, "invalid_request", "id query parameter is required")
		})
	}
	t.Run("returns service error", func(t *testing.T) {
		handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
			listAttempts: func(context.Context, string) ([]*domain.Attempt, error) {
				return nil, errors.New("database unavailable")
			},
		})
		response := httptest.NewRecorder()
		req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/deliveries/delivery-1/attempts", nil), map[string]string{"id": "delivery-1"})
		handler.ListAttempts(response, req)
		assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error", "database unavailable")
	})
}

func (s serviceStub) ListDeliveries(ctx context.Context, query application.ListDeliveriesQuery) (*application.DeliveryPage, error) {
	if s.listDeliveries != nil {
		return s.listDeliveries(ctx, query)
	}
	return &application.DeliveryPage{Deliveries: []*domain.Delivery{}}, nil
}

func TestHandlerListDeliveriesQuery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, params, status, cursor string
		limit                        uint
	}{
		{name: "defaults", limit: 20},
		{name: "empty optional parameters", params: "?status=&limit=&cursor=", limit: 20},
		{name: "pending", params: "?status=pending&limit=1", status: "pending", limit: 1},
		{name: "in progress", params: "?status=in_progress", status: "in_progress", limit: 20},
		{name: "delivered", params: "?status=delivered&limit=100", status: "delivered", limit: 100},
		{name: "all parameters", params: "?status=dead_letter&limit=25&cursor=opaque-cursor", status: "dead_letter", limit: 25, cursor: "opaque-cursor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/deliveries"+tc.params, nil)
			calls := 0
			handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
				listDeliveries: func(ctx context.Context, query application.ListDeliveriesQuery) (*application.DeliveryPage, error) {
					calls++
					if ctx != req.Context() || query.Limit != tc.limit || query.Cursor != nil {
						t.Errorf("unexpected query or context: %+v", query)
					}
					if tc.status == "" {
						if query.Status != nil {
							t.Errorf("status=%v, want nil", query.Status)
						}
					} else if query.Status == nil || string(*query.Status) != tc.status {
						t.Errorf("status=%v, want %q", query.Status, tc.status)
					}
					if tc.cursor == "" {
						if query.CursorRaw != nil {
							t.Errorf("cursor=%v, want nil", query.CursorRaw)
						}
					} else if query.CursorRaw == nil || *query.CursorRaw != tc.cursor {
						t.Errorf("cursor=%v, want %q", query.CursorRaw, tc.cursor)
					}
					return &application.DeliveryPage{Deliveries: []*domain.Delivery{}}, nil
				},
			})
			response := httptest.NewRecorder()
			handler.ListDeliveries(response, req)
			if response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
	for _, params := range []string{"?status=unknown", "?status=PENDING", "?limit=0", "?limit=-1", "?limit=101", "?limit=abc", "?limit=1.5", "?limit=18446744073709551616"} {
		t.Run("rejects "+params, func(t *testing.T) {
			// A nil service ensures invalid input is rejected before delegation.
			handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			response := httptest.NewRecorder()
			handler.ListDeliveries(response, httptest.NewRequest(http.MethodGet, "/deliveries"+params, nil))
			message := "limit must be an integer between 1 and 100"
			if strings.HasPrefix(params, "?status") {
				message = "status must be pending, in_progress, delivered, or dead_letter"
			}
			assertErrorResponse(t, response, http.StatusBadRequest, "invalid_request", message)
		})
	}
	t.Run("service error", func(t *testing.T) {
		handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
			listDeliveries: func(context.Context, application.ListDeliveriesQuery) (*application.DeliveryPage, error) {
				return nil, errors.New("database unavailable")
			},
		})
		response := httptest.NewRecorder()
		handler.ListDeliveries(response, httptest.NewRequest(http.MethodGet, "/deliveries", nil))
		assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error", "database unavailable")
	})
}

func TestHandlerListDeliveriesInvalidCursor(t *testing.T) {
	t.Parallel()
	for _, err := range []error{application.ErrInvalidCursor, fmt.Errorf("decode cursor: %w", application.ErrInvalidCursor)} {
		t.Run(err.Error(), func(t *testing.T) {
			handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), serviceStub{
				listDeliveries: func(context.Context, application.ListDeliveriesQuery) (*application.DeliveryPage, error) {
					return nil, err
				},
			})
			response := httptest.NewRecorder()
			handler.ListDeliveries(response, httptest.NewRequest(http.MethodGet, "/deliveries?cursor=bad", nil))
			assertErrorResponse(t, response, http.StatusBadRequest, "invalid_request", "invalid cursor")
		})
	}
}

func TestHandlerListDeliveriesSummary(t *testing.T) {
	t.Parallel()
	token, key := "secret-lease", "secret-key"
	expires := time.Now().UTC()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
			page := &application.DeliveryPage{NextCursor: "next-page"}
			if !empty {
				page.Deliveries = []*domain.Delivery{{ID: "delivery-1", URL: "https://example.com/hook", Payload: "private-payload",
					Headers: map[string]string{"Authorization": "Bearer secret"}, LeaseToken: &token, LeaseExpiresAt: &expires,
					IdempotencyKey: &key, Status: domain.DeliveryStatusPending, MaxAttempts: 8}}
			}
			handler := NewHandler(logger, serviceStub{listDeliveries: func(context.Context, application.ListDeliveriesQuery) (*application.DeliveryPage, error) {
				return page, nil
			}})
			response := httptest.NewRecorder()
			handler.ListDeliveries(response, httptest.NewRequest(http.MethodGet, "/deliveries", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d", response.Code)
			}
			var body struct {
				Deliveries []map[string]json.RawMessage `json:"deliveries"`
				NextCursor string                       `json:"next_cursor"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Deliveries == nil || body.NextCursor != page.NextCursor {
				t.Fatalf("invalid response: %+v", body)
			}
			if empty {
				if len(body.Deliveries) != 0 {
					t.Fatal("expected empty array")
				}
				return
			}
			if len(body.Deliveries) != 1 || string(body.Deliveries[0]["id"]) != `"delivery-1"` || string(body.Deliveries[0]["status"]) != `"pending"` {
				t.Fatalf("invalid summary: %+v", body)
			}
			for _, field := range []string{"headers", "payload", "lease_token", "lease_expires_at", "idempotency_key"} {
				if _, exists := body.Deliveries[0][field]; exists {
					t.Errorf("private field %q exposed", field)
				}
			}
		})
	}
}
