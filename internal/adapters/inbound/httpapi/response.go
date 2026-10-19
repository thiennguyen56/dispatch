package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/internal/domain"
)

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// deliverySummary contains the fields needed to browse delivery state.
type deliverySummary struct {
	ID            string                `json:"id"`
	URL           string                `json:"url"`
	Status        domain.DeliveryStatus `json:"status"`
	AttemptsMade  int                   `json:"attempts_made"`
	MaxAttempts   int                   `json:"max_attempts"`
	NextAttemptAt time.Time             `json:"next_attempt_at"`
	LastError     *string               `json:"last_error"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	DeliveredAt   *time.Time            `json:"delivered_at"`
}

type deliveryPageResponse struct {
	Deliveries []deliverySummary `json:"deliveries"`
	NextCursor string            `json:"next_cursor"`
}

func newDeliveryPageResponse(page *application.DeliveryPage) deliveryPageResponse {
	response := deliveryPageResponse{
		Deliveries: make([]deliverySummary, 0, len(page.Deliveries)),
		NextCursor: page.NextCursor,
	}
	for _, delivery := range page.Deliveries {
		response.Deliveries = append(response.Deliveries, deliverySummary{
			ID: delivery.ID, URL: delivery.URL, Status: delivery.Status,
			AttemptsMade: delivery.AttemptsMade, MaxAttempts: delivery.MaxAttempts,
			NextAttemptAt: delivery.NextAttemptAt, LastError: delivery.LastError,
			CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt,
			DeliveredAt: delivery.DeliveredAt,
		})
	}
	return response
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{
		Error: errorBody{
			Code:    code,
			Message: message,
		},
	})
}
