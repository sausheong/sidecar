package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MemorySearchResult is a single entry returned by SearchMemory.
type MemorySearchResult struct {
	Category   string
	Content    string
	Similarity float64 // cosine similarity in [0, 1]
}

// TaskEvent is a single event row from task_events.
type TaskEvent struct {
	ID        uuid.UUID
	TaskID    uuid.UUID
	Type      string
	Payload   map[string]any
	CreatedAt time.Time
}

// StoreMemory writes a memory entry with its embedding to memory_entries.
func (db *DB) StoreMemory(ctx context.Context, workspaceID uuid.UUID, category, content string, embedding []float32) error {
	vec := formatVector(embedding)
	_, err := db.pool.Exec(ctx, `
		INSERT INTO memory_entries (workspace_id, category, content, embedding)
		VALUES ($1, $2, $3, $4::vector)`,
		workspaceID, category, content, vec,
	)
	if err != nil {
		return fmt.Errorf("storing memory: %w", err)
	}
	return nil
}

// SearchMemory returns the top-k most similar memory entries for the given query embedding.
// categories filters results (e.g. []string{"semantic","procedural"}).
func (db *DB) SearchMemory(ctx context.Context, workspaceID uuid.UUID, categories []string, queryEmbedding []float32, limit int) ([]*MemorySearchResult, error) {
	vec := formatVector(queryEmbedding)
	rows, err := db.pool.Query(ctx, `
		SELECT category, content, 1 - (embedding <=> $1::vector) AS similarity
		FROM memory_entries
		WHERE workspace_id = $2
		  AND category = ANY($3)
		ORDER BY embedding <=> $1::vector
		LIMIT $4`,
		vec, workspaceID, categories, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching memory: %w", err)
	}
	defer rows.Close()

	var results []*MemorySearchResult
	for rows.Next() {
		r := &MemorySearchResult{}
		if err := rows.Scan(&r.Category, &r.Content, &r.Similarity); err != nil {
			return nil, fmt.Errorf("scanning memory row: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetPolicies returns all policy rules for a workspace, ordered by creation time.
func (db *DB) GetPolicies(ctx context.Context, workspaceID uuid.UUID) ([]string, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT rule FROM policies WHERE workspace_id = $1 ORDER BY created_at`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading policies: %w", err)
	}
	defer rows.Close()

	var policies []string
	for rows.Next() {
		var rule string
		if err := rows.Scan(&rule); err != nil {
			return nil, fmt.Errorf("scanning policy: %w", err)
		}
		policies = append(policies, rule)
	}
	return policies, rows.Err()
}

// StorePolicy writes a policy rule. source is "yaml" or "learned".
// Duplicate (workspace_id, rule) pairs are silently ignored.
func (db *DB) StorePolicy(ctx context.Context, workspaceID uuid.UUID, rule, source string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO policies (workspace_id, rule, source) VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, rule) DO NOTHING`,
		workspaceID, rule, source,
	)
	if err != nil {
		return fmt.Errorf("storing policy: %w", err)
	}
	return nil
}

// GetTaskEvents returns all task_events for a task, ordered by created_at.
func (db *DB) GetTaskEvents(ctx context.Context, taskID uuid.UUID) ([]*TaskEvent, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, task_id, type, payload, created_at
		FROM task_events
		WHERE task_id = $1
		ORDER BY created_at`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading task events: %w", err)
	}
	defer rows.Close()

	var events []*TaskEvent
	for rows.Next() {
		ev := &TaskEvent{}
		var payloadJSON []byte
		if err := rows.Scan(&ev.ID, &ev.TaskID, &ev.Type, &payloadJSON, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning task event: %w", err)
		}
		if err := json.Unmarshal(payloadJSON, &ev.Payload); err != nil {
			return nil, fmt.Errorf("unmarshaling task event payload %s: %w", ev.ID, err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// MemoryRow is a materialized memory_entries row, returned by GetMemory
// and ListMemory. Embedding is omitted because adapter callers don't
// need it post-write.
type MemoryRow struct {
	ID        uuid.UUID
	Category  string
	Content   string
	Origin    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// StoreMemoryReturning inserts a memory entry and returns its id and
// created_at timestamp. Origin is "agent", "review", or any caller
// label; persisted in the origin column.
func (db *DB) StoreMemoryReturning(
	ctx context.Context,
	workspaceID uuid.UUID,
	category, content, origin string,
	embedding []float32,
) (uuid.UUID, time.Time, error) {
	vec := formatVector(embedding)
	var id uuid.UUID
	var createdAt time.Time
	err := db.pool.QueryRow(ctx, `
		INSERT INTO memory_entries (workspace_id, category, content, origin, embedding)
		VALUES ($1, $2, $3, $4, $5::vector)
		RETURNING id, created_at`,
		workspaceID, category, content, origin, vec,
	).Scan(&id, &createdAt)
	if err != nil {
		return uuid.Nil, time.Time{}, fmt.Errorf("storing memory: %w", err)
	}
	return id, createdAt, nil
}

// formatVector converts []float32 to PostgreSQL vector literal "[f1,f2,...]".
func formatVector(v []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}
