package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type Service interface {

}

type Handler struct {
	logger *slog.Logger
	service Service
}

func NewHandler(logger *slog.Logger, service Service) *Handler {
	return &Handler{logger: logger, service: service}
}

func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	var input submitDeliveries
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		h.logger.Error("failed to decode request", "error", err)
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
		return
	}
	if err := input.IsValid(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	h.logger.Info("submit deliveries", "input", input)

	writeJSON(w, http.StatusCreated, map[string]any{
		"message": "Hello World!",
	})
}
