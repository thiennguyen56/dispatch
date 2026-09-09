package bootstrap

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/thiennguyen56/dispatch/internal/adapters/inbound/httpapi"
	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/pkg/logger"
)

type App struct {
}

func NewApp() *App {
	return &App{}
}

func (a *App) Run() error {
	log := logger.New(logger.Config{
		Service: "dispatch",
		Level:   slog.LevelInfo,
		JSON:    true,
	})
	service := application.NewService(log)
	router := httpapi.NewRouter(log, service)
	srv := &http.Server{
		Handler:      router.DeliveryRouter(),
		Addr:         ":8080",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 & time.Second,
	}

	log.Info("server started", "addr", srv.Addr)
	return srv.ListenAndServe()
}
