package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type Workspace struct {
	ID         uuid.UUID
	Name       string
	Path       string
	ConfigHash string
}

func (db *DB) UpsertWorkspace(ctx context.Context, ws *Workspace) error {
	row := db.pool.QueryRow(ctx, `
		INSERT INTO workspaces (name, path, config_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (path) DO UPDATE
		  SET name = EXCLUDED.name,
		      config_hash = EXCLUDED.config_hash
		RETURNING id`,
		ws.Name, ws.Path, ws.ConfigHash,
	)
	return row.Scan(&ws.ID)
}

func (db *DB) GetWorkspaceByPath(ctx context.Context, path string) (*Workspace, error) {
	ws := &Workspace{}
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, path, config_hash FROM workspaces WHERE path = $1`, path,
	).Scan(&ws.ID, &ws.Name, &ws.Path, &ws.ConfigHash)
	if err != nil {
		return nil, fmt.Errorf("getting workspace: %w", err)
	}
	return ws, nil
}
