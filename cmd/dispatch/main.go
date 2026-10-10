package main

import (
	"log/slog"
	"os"

	"github.com/thiennguyen56/dispatch/internal/bootstrap"
	"github.com/thiennguyen56/dispatch/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	if err := bootstrap.NewApp(cfg).Run(); err != nil {
		slog.Error("dispatch stopped", "error", err)
		os.Exit(1)
	}
}
