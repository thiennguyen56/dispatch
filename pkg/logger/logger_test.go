package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/thiennguyen56/dispatch/pkg/logger"
)

func TestNewWithWriterJSONIncludesConfiguredFields(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	log := logger.NewWithWriter(&output, logger.Config{
		Service: "dispatch-api",
		Level:   slog.LevelInfo,
		JSON:    true,
	})

	log.Info("request completed", "status", 200)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode JSON log: %v\noutput: %s", err, output.String())
	}

	if got, want := entry["msg"], "request completed"; got != want {
		t.Errorf("message = %v, want %q", got, want)
	}
	if got, want := entry["level"], "INFO"; got != want {
		t.Errorf("level = %v, want %q", got, want)
	}
	if got, want := entry["service"], "dispatch-api"; got != want {
		t.Errorf("service = %v, want %q", got, want)
	}
	if got, want := entry["status"], float64(200); got != want {
		t.Errorf("status = %v, want %v", got, want)
	}
}

func TestNewWithWriterTextOmitsEmptyService(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	log := logger.NewWithWriter(&output, logger.Config{Level: slog.LevelInfo})

	log.Info("worker started", "queue", "emails")

	got := output.String()
	for _, want := range []string{"level=INFO", "msg=\"worker started\"", "queue=emails"} {
		if !strings.Contains(got, want) {
			t.Errorf("text log %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "service=") {
		t.Errorf("text log %q unexpectedly contains service", got)
	}
}

func TestNewWithWriterHonorsLevel(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	log := logger.NewWithWriter(&output, logger.Config{Level: slog.LevelWarn})

	log.Info("not written")
	log.Warn("written")

	got := output.String()
	if strings.Contains(got, "not written") {
		t.Errorf("info log was written at warn level: %q", got)
	}
	if !strings.Contains(got, "msg=written") {
		t.Errorf("warn log was not written: %q", got)
	}
}

func TestNewWithWriterAddsSourceWhenConfigured(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	log := logger.NewWithWriter(&output, logger.Config{
		Level:     slog.LevelInfo,
		JSON:      true,
		AddSource: true,
	})

	log.Info("with source")

	var entry struct {
		Source struct {
			File string `json:"file"`
			Line int    `json:"line"`
		} `json:"source"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode JSON log: %v\noutput: %s", err, output.String())
	}
	if !strings.HasSuffix(entry.Source.File, "logger_test.go") {
		t.Errorf("source file = %q, want logger_test.go", entry.Source.File)
	}
	if entry.Source.Line == 0 {
		t.Error("source line = 0, want a positive line number")
	}
}
