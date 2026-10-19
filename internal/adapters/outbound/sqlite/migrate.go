package sqlite

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migration/*.sql
var migrations embed.FS

// Migrate applies pending migrations before the API and worker start.
// The caller owns db and remains responsible for closing it.
func Migrate(db *sql.DB) error {
	source, err := iofs.New(migrations, "migration")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	defer source.Close()

	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	if err != nil {
		return fmt.Errorf("initialize migration database: %w", err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	// Do not call runner.Close(): it would also close the caller's database.
	if err := runner.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
