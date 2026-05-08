package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Task struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	SignalType  string
	Status      string
	Summary     string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (db *DB) CreateTask(ctx context.Context, t *Task) error {
	if t.Status == "" {
		t.Status = "pending"
	}
	row := db.pool.QueryRow(ctx, `
		INSERT INTO tasks (workspace_id, signal_type, status, summary)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		t.WorkspaceID, t.SignalType, t.Status, t.Summary,
	)
	return row.Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
}

func (db *DB) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE tasks SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("updating task status: %w", err)
	}
	return nil
}

func (db *DB) ListTasks(ctx context.Context, workspaceID uuid.UUID, limit int) ([]*Task, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, workspace_id, signal_type, status, summary, created_at, updated_at
		FROM tasks
		WHERE workspace_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(&t.ID, &t.WorkspaceID, &t.SignalType, &t.Status,
			&t.Summary, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
