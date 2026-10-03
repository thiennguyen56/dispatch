package main

import (
	"log/slog"
	"os"

	"github.com/thiennguyen56/dispatch/internal/bootstrap"
	"github.com/thiennguyen56/dispatch/internal/config"
)

func main() {

	if err := bootstrap.NewApp(config.Default()).Run(); err != nil {
		slog.Error("dispatch stopped", "error", err)
		os.Exit(1)
	}
}
