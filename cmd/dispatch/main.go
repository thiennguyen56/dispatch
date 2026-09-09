package main

import (
	"github.com/thiennguyen56/dispatch/internal/bootstrap"
	"github.com/thiennguyen56/dispatch/internal/config"
)

func main() {
	bootstrap.NewApp(config.Default()).Run()
}
