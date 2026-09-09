package bootstrap

import (
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/thiennguyen56/dispatch/internal/adapters/inbound/httpapi"
	"github.com/thiennguyen56/dispatch/internal/adapters/outbound/sqlite"
	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/pkg/logger"

	_ "modernc.org/sqlite" // Import the driver anonymously
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

	db, err := sql.Open("sqlite", "dispatch.db")
	if err != nil {
		log.Error("failed to open database", "error", err)
		return err
	}
	repo := sqlite.NewRepository(db, log)
	service := application.NewService(log, repo)
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
