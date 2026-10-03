package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type Service interface {
	Submit(ctx context.Context, input application.InputSubmit) (*domain.Delivery, error)
	Get(ctx context.Context, id string) (*domain.Delivery, error)
}

type Handler struct {
	logger  *slog.Logger
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

	var idempotencyKey *string
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		idempotencyKey = &key
	}
	h.logger.Info("submit deliveries", "input", input)
	deliveryRow, err := h.service.Submit(r.Context(), application.InputSubmit{
		URL:            input.URL,
		Payload:        input.Payload,
		Headers:        input.Headers,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		h.logger.Error("failed to submit deliveries", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":      deliveryRow.ID,
		"message": "Submitted deliveries",
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "id query parameter is required")
		return
	}

	delivery, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.logger.Error("failed to get delivery", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, delivery)
}
