package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

const (
	defaultTimeout        = 10 * time.Second
	maxResponseDrainBytes = 64 << 10
)

type Sender struct {
	logger *slog.Logger
	http   *http.Client
}

var _ application.Sender = (*Sender)(nil)

func NewSender(logger *slog.Logger) *Sender {
	return &Sender{
		logger: logger,
		http: &http.Client{
			Timeout: defaultTimeout,
			// Keep the attempt at the submitted destination and preserve 3xx
			// responses for the processor instead of silently following them.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (s *Sender) Send(ctx context.Context, delivery domain.Delivery) (result application.SendResult, err error) {
	started := time.Now()
	result.StartedAt = started.UTC()
	result.Outcome = domain.AttemptOutcomeFailed
	defer func() {
		finished := time.Now()
		result.FinishedAt = finished.UTC()
		result.Duration = finished.Sub(started)
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
			result.Outcome = domain.AttemptOutcomeTimedOut
		}
		// Never log payloads, headers, URLs, or raw transport errors here.
		s.logger.DebugContext(ctx, "webhook attempt finished",
			"delivery_id", delivery.ID, "outcome", result.Outcome,
			"duration", result.Duration)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URL, strings.NewReader(delivery.Payload))
	if err != nil {
		return result, fmt.Errorf("build webhook request: %w", err)
	}
	if (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" {
		return result, errors.New("webhook URL must be an absolute HTTP or HTTPS URL")
	}
	for name, value := range delivery.Headers {
		req.Header.Set(name, value)
	}

	req.Header.Set("X-Dispatch-Delivery-ID", delivery.ID)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := s.http.Do(req)
	if err != nil {
		return result, fmt.Errorf("send webhook request: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	status := response.StatusCode
	result.ResponseStatus = &status
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		result.Outcome = domain.AttemptOutcomeSucceeded
	}

	// Best-effort bounded draining allows connection reuse for small replies.
	// The client timeout also bounds body reads. Once headers acknowledge the
	// attempt, a body read error must not turn a 2xx response into another send.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseDrainBytes))
	return result, nil
}
