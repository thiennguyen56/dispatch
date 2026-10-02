package bootstrap

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thiennguyen56/dispatch/internal/adapters/inbound/httpapi"
	"github.com/thiennguyen56/dispatch/internal/adapters/inbound/worker"
	"github.com/thiennguyen56/dispatch/internal/adapters/outbound/sqlite"
	"github.com/thiennguyen56/dispatch/internal/adapters/outbound/webhook"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
	sender := webhook.NewSender(log)
	processor := application.NewProcessor(log, repo, sender)
	worker := worker.NewWorker(log, processor, 5*time.Second)
	go func() {
		worker.Run(ctx)
	}()

	router := httpapi.NewRouter(log, service)
	srv := &http.Server{
		Handler:      router.DeliveryRouter(),
		Addr:         a.config.Server.Address,
		WriteTimeout: a.config.Server.WriteTimeout,
		ReadTimeout:  a.config.Server.ReadTimeout,
	}

	log.Info("server started", "addr", srv.Addr)
	go func() {
		srv.ListenAndServe()
	}()

	<-ctx.Done()
	log.Info("Shutdown signal received. Starting graceful shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Info("Server forced to shutdown", "error", err)
	} else {
		log.Info("Server stopped gracefully.")
	}

	if err := db.Close(); err != nil {
		log.Info("failed to close database", "error", err)
	}
	return nil
}
