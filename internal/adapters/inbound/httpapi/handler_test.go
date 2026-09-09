package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

	handler := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
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
	if got, want := body["message"], "Hello World!"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
