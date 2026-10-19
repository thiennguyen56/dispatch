package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type Service interface {
	Submit(ctx context.Context, input application.InputSubmit) (*domain.Delivery, error)
	Get(ctx context.Context, id string) (*domain.Delivery, error)
	ListAttempts(ctx context.Context, deliveryID string) ([]*domain.Attempt, error)
	ListDeliveries(ctx context.Context, query application.ListDeliveriesQuery) (*application.DeliveryPage, error)
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

func (h *Handler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	query := application.ListDeliveriesQuery{Limit: 20}

	if rawStatus := params.Get("status"); rawStatus != "" {
		status := domain.DeliveryStatus(rawStatus)
		switch status {
		case domain.DeliveryStatusPending, domain.DeliveryStatusInProgress,
			domain.DeliveryStatusDelivered, domain.DeliveryStatusDeadLetter:
			query.Status = &status
		default:
			writeError(w, http.StatusBadRequest, "invalid_request",
				"status must be pending, in_progress, delivered, or dead_letter")
			return
		}
	}

	if rawLimit := params.Get("limit"); rawLimit != "" {
		limit, err := strconv.ParseUint(rawLimit, 10, 32)
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"limit must be an integer between 1 and 100")
			return
		}
		query.Limit = uint(limit)
	}

	if cursor := params.Get("cursor"); cursor != "" {
		query.CursorRaw = &cursor
	}
	deliveries, err := h.service.ListDeliveries(r.Context(), query)
	if err != nil {
		if errors.Is(err, application.ErrInvalidCursor) {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid cursor")
			return
		}
		h.logger.Error("failed to list deliveries", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, newDeliveryPageResponse(deliveries))
}

func (h *Handler) ListAttempts(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deliveryID := vars["id"]
	if strings.TrimSpace(deliveryID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "id query parameter is required")
		return
	}
	attempts, err := h.service.ListAttempts(r.Context(), deliveryID)
	if err != nil {
		h.logger.Error("failed to list attempts from service layer", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, attempts)
}
