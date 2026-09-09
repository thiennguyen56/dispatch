package application

import "log/slog"

type Processor struct {
	logger *slog.Logger
}

func NewProcessor(logger *slog.Logger) *Processor {
	return &Processor{logger: logger}
}
