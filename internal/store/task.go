package store

import (
	"context"
	"encoding/json"
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
	tag, err := db.pool.Exec(ctx, `
		UPDATE tasks SET status = $1, updated_at = NOW() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("updating task status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task %s not found", id)
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

// AppendTaskEvent records a single event for a task in the task_events table.
// payload is marshaled to JSONB.
func (db *DB) AppendTaskEvent(ctx context.Context, taskID uuid.UUID, eventType string, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling event payload: %w", err)
	}
	_, err = db.pool.Exec(ctx, `
		INSERT INTO task_events (task_id, type, payload)
		VALUES ($1, $2, $3)`,
		taskID, eventType, data,
	)
	if err != nil {
		return fmt.Errorf("appending task event: %w", err)
	}
	return nil
}
