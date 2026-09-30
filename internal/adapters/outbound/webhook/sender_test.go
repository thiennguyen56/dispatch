package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

func testSender() *Sender {
	return NewSender(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSendPreservesRequest(t *testing.T) {
	for _, contentType := range []string{"", "text/plain"} {
		t.Run(fmt.Sprintf("content_type=%s", contentType), func(t *testing.T) {
			payload := "{\"event\": \"created\"}\n"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if r.Method != http.MethodPost || string(body) != payload || r.URL.RequestURI() != "/hook?source=dispatch" {
					t.Errorf("unexpected request: %s %s body=%q", r.Method, r.URL, body)
				}
				wantType := contentType
				if wantType == "" {
					wantType = "application/json"
				}
				if r.Header.Get("Content-Type") != wantType || r.Header.Get("Authorization") != "Bearer test" {
					t.Errorf("unexpected headers: %v", r.Header)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			headers := map[string]string{"Authorization": "Bearer test"}
			if contentType != "" {
				headers["content-type"] = contentType
			}
			before := time.Now()
			result, err := testSender().Send(context.Background(), domain.Delivery{
				URL: server.URL + "/hook?source=dispatch", Payload: payload, Headers: headers,
			})
			if err != nil || result.Outcome != domain.AttemptOutcomeSucceeded {
				t.Fatalf("Send() = %+v, %v", result, err)
			}
			if result.ResponseStatus == nil || *result.ResponseStatus != http.StatusNoContent {
				t.Fatalf("unexpected status: %v", result.ResponseStatus)
			}
			if result.StartedAt.Before(before) || result.FinishedAt.Before(result.StartedAt) || result.FinishedAt.After(time.Now()) || result.Duration < 0 {
				t.Errorf("invalid timing: %+v", result)
			}
			if contentType == "" && len(headers) != 1 {
				t.Errorf("Send mutated the delivery headers: %v", headers)
			}
		})
	}
}

func TestSendClassifiesHTTPResponsesWithoutRetryingOrRedirecting(t *testing.T) {
	for _, status := range []int{200, 202, 204, 299, 301, 302, 307, 308, 400, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/redirected" {
					t.Error("sender followed a redirect")
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
			}))
			defer server.Close()
			result, err := testSender().Send(context.Background(), domain.Delivery{URL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			want := domain.AttemptOutcomeFailed
			if status < 300 {
				want = domain.AttemptOutcomeSucceeded
			}
			if result.Outcome != want || result.ResponseStatus == nil || *result.ResponseStatus != status {
				t.Errorf("Send() = %+v, want outcome %s and status %d", result, want, status)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("sent %d requests, want 1", got)
			}
		})
	}
}

func TestSendTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"client_timeout", "context_deadline", "context_cancel"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			sender := testSender()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			wantOutcome := domain.AttemptOutcomeTimedOut
			wantError := context.DeadlineExceeded
			switch mode {
			case "client_timeout":
				sender.http.Timeout = 50 * time.Millisecond
			case "context_deadline":
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer deadlineCancel()
			case "context_cancel":
				wantOutcome = domain.AttemptOutcomeFailed
				wantError = context.Canceled
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result, err := sender.Send(ctx, domain.Delivery{URL: server.URL})
			if !errors.Is(err, wantError) || result.Outcome != wantOutcome || result.ResponseStatus != nil {
				t.Fatalf("Send() = %+v, %v; want %s / %v", result, err, wantOutcome, wantError)
			}
			if result.FinishedAt.IsZero() || result.Duration <= 0 {
				t.Errorf("missing failure timing: %+v", result)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestSendTransportFailure(t *testing.T) {
	sender := testSender()
	failure := errors.New("connection unavailable")
	sender.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, failure
	})
	result, err := sender.Send(context.Background(), domain.Delivery{URL: "https://example.com"})
	if !errors.Is(err, failure) || result.Outcome != domain.AttemptOutcomeFailed || result.ResponseStatus != nil {
		t.Fatalf("Send() = %+v, %v", result, err)
	}
}

func TestSendRejectsInvalidURLs(t *testing.T) {
	for _, target := range []string{"://bad", "/relative", "ftp://example.com"} {
		t.Run(target, func(t *testing.T) {
			sender := testSender()
			sender.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("invalid URL reached transport")
				return nil, errors.New("unexpected request")
			})
			result, err := sender.Send(context.Background(), domain.Delivery{URL: target})
			if err == nil || result.Outcome != domain.AttemptOutcomeFailed || result.ResponseStatus != nil {
				t.Fatalf("Send() = %+v, %v", result, err)
			}
		})
	}
}

type trackedBody struct {
	reader io.Reader
	read   int
	closed bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestSendBoundsAndClosesResponseBody(t *testing.T) {
	sender := testSender()
	body := &trackedBody{reader: strings.NewReader(strings.Repeat("x", 2*maxResponseDrainBytes))}
	sender.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
	})
	result, err := sender.Send(context.Background(), domain.Delivery{URL: "https://example.com"})
	if err != nil || result.Outcome != domain.AttemptOutcomeSucceeded {
		t.Fatalf("Send() = %+v, %v", result, err)
	}
	if !body.closed || body.read != maxResponseDrainBytes {
		t.Errorf("body closed=%t, bytes read=%d", body.closed, body.read)
	}
}
