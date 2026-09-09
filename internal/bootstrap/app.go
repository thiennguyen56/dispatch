package bootstrap

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/thiennguyen56/dispatch/internal/adapters/inbound/httpapi"
	"github.com/thiennguyen56/dispatch/internal/adapters/outbound/sqlite"
	"github.com/thiennguyen56/dispatch/internal/application"
	"github.com/thiennguyen56/dispatch/internal/config"
	"github.com/thiennguyen56/dispatch/pkg/logger"

	_ "modernc.org/sqlite" // Import the driver anonymously
)

type App struct {
	config config.Config
}

func NewApp(appConfig config.Config) *App {
	return &App{config: appConfig}
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
		Addr:         a.config.Server.Address,
		WriteTimeout: a.config.Server.WriteTimeout,
		ReadTimeout:  a.config.Server.ReadTimeout,
	}

	log.Info("server started", "addr", srv.Addr)
	return srv.ListenAndServe()
}
