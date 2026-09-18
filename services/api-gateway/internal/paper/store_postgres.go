package paper

import (
	"context"
	"database/sql"
)

type PostgresStore struct {
	db *sql.DB
}

type postgresQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}
