package logger

import (
	"io"
	"log/slog"
	"os"
)

type Config struct {
	Service   string
	Level     slog.Level
	JSON      bool
	AddSource bool
}

func New(cfg Config) *slog.Logger {
	return NewWithWriter(os.Stdout, cfg)
}

// NewWithWriter allows tests to capture logs in a buffer.
func NewWithWriter(w io.Writer, cfg Config) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: cfg.AddSource,
	}

	var handler slog.Handler
	if cfg.JSON {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	log := slog.New(handler)
	if cfg.Service != "" {
		log = log.With("service", cfg.Service)
	}
	return log
}
