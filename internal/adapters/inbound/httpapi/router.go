package httpapi

import (
	"log/slog"

	"github.com/gorilla/mux"
)

type Router struct {
	logger  *slog.Logger
	service Service
}

func NewRouter(logger *slog.Logger, service Service) *Router {
	return &Router{
		logger:  logger,
		service: service,
	}
}

func (r *Router) DeliveryRouter() *mux.Router {
	handler := NewHandler(r.logger, r.service)
	router := mux.NewRouter()
	router.HandleFunc("/deliveries", handler.Submit).Methods("POST")
	router.HandleFunc("/deliveries/{id}", handler.Get).Methods("GET")

	return router
}
