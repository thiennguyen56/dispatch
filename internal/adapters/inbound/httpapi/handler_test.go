package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type serviceStub struct {
	listAttempts func(context.Context, string) ([]*domain.Attempt, error)
	delivery     *domain.Delivery
	submitErr    error
	getErr       error
	submit       func(context.Context, application.InputSubmit) (*domain.Delivery, error)
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
