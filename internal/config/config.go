package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server   ServerConfig
	Database DataBaseConfig
}

type ServerConfig struct {
	Address      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type DataBaseConfig struct {
	URL string
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Address:      ":8080",
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
		},
		Database: DataBaseConfig{
			URL: "dispatch.db",
		},
	}
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	cfg := Default()

	if value := os.Getenv("DISPATCH_DB_PATH"); value != "" {
		cfg.Database.URL = value
	}
	if value := os.Getenv("DISPATCH_SERVER_ADDRESS"); value != "" {
		cfg.Server.Address = value
	}

	return cfg, nil
}
