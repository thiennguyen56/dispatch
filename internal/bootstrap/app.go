package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	workerCtx, cancelWorker := context.WithCancel(context.Background())

	workerDone := make(chan error, 1)
	go func() {
		workerDone <- worker.Run(workerCtx)
	}()

	router := httpapi.NewRouter(log, service)
	srv := &http.Server{
		Handler:      router.DeliveryRouter(),
		Addr:         a.config.Server.Address,
		WriteTimeout: a.config.Server.WriteTimeout,
		ReadTimeout:  a.config.Server.ReadTimeout,
	}

	serverDone := make(chan error, 1)
	log.Info("server started", "addr", srv.Addr)
	go func() {
		serverDone <- srv.ListenAndServe()
	}()

	var runErr error

	select {
	case <-ctx.Done():
	case err := <-serverDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("HTTP server failed: %w", err)
		}
	}
	cancelWorker()

	return errors.Join(runErr, shutdown(srv, workerDone, db))
}

func shutdown(srv *http.Server, workerDone chan error, db *sql.DB) error {
	var runErr error
	slog.Info("Shutdown signal received. Starting graceful shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Info("Server forced to shutdown", "error", err)
		runErr = errors.Join(runErr, fmt.Errorf("shutdown HTTP server: %w", err))
		_ = srv.Close() // Force-close remaining HTTP connections.
	} else {
		slog.Info("Server stopped gracefully.")
	}

	select {
	case err := <-workerDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = errors.Join(runErr, fmt.Errorf("stop worker: %w", err))
		}
	case <-shutdownCtx.Done():
		runErr = errors.Join(runErr, errors.New("worker shutdown timed out"))
	}

	if err := db.Close(); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("close database: %w", err))
	}
	return runErr
}
