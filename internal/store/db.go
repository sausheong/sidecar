package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

//go:embed schema.sql
var schema string

type DB struct {
	pool *pgxpool.Pool
}

func Connect(ctx context.Context, dsn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging postgres: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the underlying pgx pool for callers that need direct
// query access (typically tests).
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

func Migrate(ctx context.Context, db *DB) error {
	_, err := db.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}
