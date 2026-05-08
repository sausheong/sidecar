package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Workspace struct {
	ID         uuid.UUID
	Name       string
	Path       string
	ConfigHash string
	CreatedAt  time.Time
}

func (db *DB) UpsertWorkspace(ctx context.Context, ws *Workspace) error {
	row := db.pool.QueryRow(ctx, `
		INSERT INTO workspaces (name, path, config_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (path) DO UPDATE
		  SET name = EXCLUDED.name,
		      config_hash = EXCLUDED.config_hash
		RETURNING id, created_at`,
		ws.Name, ws.Path, ws.ConfigHash,
	)
	return row.Scan(&ws.ID, &ws.CreatedAt)
}

func (db *DB) GetWorkspaceByPath(ctx context.Context, path string) (*Workspace, error) {
	ws := &Workspace{}
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, path, config_hash, created_at FROM workspaces WHERE path = $1`, path,
	).Scan(&ws.ID, &ws.Name, &ws.Path, &ws.ConfigHash, &ws.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("workspace %q: %w", path, ErrNotFound)
		}
		return nil, fmt.Errorf("getting workspace: %w", err)
	}
	return ws, nil
}
