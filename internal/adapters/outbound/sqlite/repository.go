package sqlite

import (
	"context"
	"database/sql"

	"github.com/thiennguyen56/dispatch/internal/domain"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(
	ctx context.Context,
	d domain.Delivery,
) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO deliveries (id, url, payload, status)
         VALUES (?, ?, ?, ?)`,
		d.ID, d.URL, d.Payload, d.Status,
	)
	return err
}
