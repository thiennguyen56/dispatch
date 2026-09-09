package webhook

import (
	"log/slog"
	"net/http"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

type Sender struct {
	logger *slog.Logger
	http   *http.Client
}

func NewSender(logger *slog.Logger) *Sender {
	return &Sender{logger: logger, http: &http.Client{}}
}

func (s *Sender) Send(delivery domain.Delivery) error {
	return nil
}
