//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sausheong/sidecar/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dbURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SIDECAR_TEST_DB_URL")
	if url == "" {
		t.Skip("SIDECAR_TEST_DB_URL not set")
	}
	return url
}

func TestConnect(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	assert.NotNil(t, db)
}

func TestMigrate(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()

	err = store.Migrate(context.Background(), db)
	require.NoError(t, err)

	// Running migrate twice is idempotent
	err = store.Migrate(context.Background(), db)
	require.NoError(t, err)
}

func TestWorkspace_UpsertAndGet(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{
		Name:       "test-service",
		Path:       t.TempDir(),
		ConfigHash: "abc123",
	}
	err = db.UpsertWorkspace(context.Background(), ws)
	require.NoError(t, err)
	assert.NotEmpty(t, ws.ID)

	got, err := db.GetWorkspaceByPath(context.Background(), ws.Path)
	require.NoError(t, err)
	assert.Equal(t, "test-service", got.Name)
	assert.Equal(t, "abc123", got.ConfigHash)

	// Upsert again with updated hash — idempotent
	ws.ConfigHash = "def456"
	err = db.UpsertWorkspace(context.Background(), ws)
	require.NoError(t, err)

	got, err = db.GetWorkspaceByPath(context.Background(), ws.Path)
	require.NoError(t, err)
	assert.Equal(t, "def456", got.ConfigHash)
}

func TestTask_CreateAndList(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	task := &store.Task{
		WorkspaceID: ws.ID,
		SignalType:  "git.commit",
		Summary:     "fix: patched auth handler",
	}
	err = db.CreateTask(context.Background(), task)
	require.NoError(t, err)
	assert.NotEmpty(t, task.ID)
	assert.Equal(t, "pending", task.Status)

	tasks, err := db.ListTasks(context.Background(), ws.ID, 10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "git.commit", tasks[0].SignalType)

	err = db.UpdateTaskStatus(context.Background(), task.ID, "completed")
	require.NoError(t, err)

	tasks, err = db.ListTasks(context.Background(), ws.ID, 10)
	require.NoError(t, err)
	assert.Equal(t, "completed", tasks[0].Status)
}

func TestTaskEvent_Append(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "evt-svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	task := &store.Task{WorkspaceID: ws.ID, SignalType: "ci.failure", Summary: "test"}
	require.NoError(t, db.CreateTask(context.Background(), task))

	payload := map[string]any{"should_act": true, "change_type": "test_fix", "reason": "tests failing"}
	err = db.AppendTaskEvent(context.Background(), task.ID, "triage", payload)
	require.NoError(t, err)
}

func TestMigrate_MemoryTables(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()

	// Should succeed and be idempotent
	require.NoError(t, store.Migrate(context.Background(), db))
	require.NoError(t, store.Migrate(context.Background(), db))
}

func TestMigrate_MemoryEntries_NewColumns(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	var hasUpdatedAt, hasOrigin bool
	err = db.Pool().QueryRow(context.Background(), `
        SELECT
            EXISTS (SELECT 1 FROM information_schema.columns
                    WHERE table_name='memory_entries' AND column_name='updated_at'),
            EXISTS (SELECT 1 FROM information_schema.columns
                    WHERE table_name='memory_entries' AND column_name='origin')
    `).Scan(&hasUpdatedAt, &hasOrigin)
	require.NoError(t, err)
	assert.True(t, hasUpdatedAt, "memory_entries should have updated_at column")
	assert.True(t, hasOrigin, "memory_entries should have origin column")
}

func TestStoreMemory_AndSearch(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "mem-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	embedding := make([]float32, 1536)
	embedding[0] = 1.0
	err = db.StoreMemory(context.Background(), ws.ID, "semantic", "auth uses interface mocking", embedding)
	require.NoError(t, err)

	results, err := db.SearchMemory(context.Background(), ws.ID, []string{"semantic", "procedural"}, embedding, 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "semantic", results[0].Category)
	assert.Equal(t, "auth uses interface mocking", results[0].Content)
	assert.Greater(t, results[0].Similarity, 0.9)
}

func TestStorePolicy_AndGet(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "pol-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	require.NoError(t, db.StorePolicy(context.Background(), ws.ID, "prefer explicit error handling", "yaml"))
	require.NoError(t, db.StorePolicy(context.Background(), ws.ID, "never touch secrets/", "yaml"))

	policies, err := db.GetPolicies(context.Background(), ws.ID)
	require.NoError(t, err)
	assert.Len(t, policies, 2)
	assert.Contains(t, policies, "prefer explicit error handling")
}

func TestGetTaskEvents(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "evt-read-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	task := &store.Task{WorkspaceID: ws.ID, SignalType: "git.commit", Summary: "test"}
	require.NoError(t, db.CreateTask(context.Background(), task))
	require.NoError(t, db.AppendTaskEvent(context.Background(), task.ID, "triage",
		map[string]any{"should_act": true, "change_type": "bug_fix"}))

	events, err := db.GetTaskEvents(context.Background(), task.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "triage", events[0].Type)
	assert.Equal(t, true, events[0].Payload["should_act"])
}

func TestStoreMemoryReturning_RoundTrip(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	embedding := make([]float32, 1024)
	embedding[0] = 0.5

	id, createdAt, err := db.StoreMemoryReturning(
		context.Background(), ws.ID, "semantic", "auth uses interface mocking", "review", embedding,
	)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, id)
	assert.False(t, createdAt.IsZero())
}

func TestGetMemory_RoundTripAndNotFound(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	embedding := make([]float32, 1024)
	id, _, err := db.StoreMemoryReturning(
		context.Background(), ws.ID, "procedural", "run go test ./...", "agent", embedding,
	)
	require.NoError(t, err)

	row, err := db.GetMemory(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "procedural", row.Category)
	assert.Equal(t, "run go test ./...", row.Content)
	assert.Equal(t, "agent", row.Origin)
	assert.False(t, row.CreatedAt.IsZero())
	assert.False(t, row.UpdatedAt.IsZero())

	_, err = db.GetMemory(context.Background(), uuid.New())
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteMemory_RoundTripAndIdempotent(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	embedding := make([]float32, 1024)
	id, _, err := db.StoreMemoryReturning(
		context.Background(), ws.ID, "semantic", "x", "agent", embedding,
	)
	require.NoError(t, err)

	require.NoError(t, db.DeleteMemory(context.Background(), id))
	_, err = db.GetMemory(context.Background(), id)
	assert.ErrorIs(t, err, store.ErrNotFound)

	// Delete again — idempotent
	require.NoError(t, db.DeleteMemory(context.Background(), id))
}

func TestListMemory_AllAndByCategory(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "svc", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	embedding := make([]float32, 1024)
	_, _, err = db.StoreMemoryReturning(context.Background(), ws.ID, "semantic", "a", "agent", embedding)
	require.NoError(t, err)
	_, _, err = db.StoreMemoryReturning(context.Background(), ws.ID, "semantic", "b", "agent", embedding)
	require.NoError(t, err)
	_, _, err = db.StoreMemoryReturning(context.Background(), ws.ID, "procedural", "c", "review", embedding)
	require.NoError(t, err)

	all, err := db.ListMemory(context.Background(), ws.ID, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	semantic, err := db.ListMemory(context.Background(), ws.ID, "semantic")
	require.NoError(t, err)
	assert.Len(t, semantic, 2)
	for _, r := range semantic {
		assert.Equal(t, "semantic", r.Category)
	}

	procedural, err := db.ListMemory(context.Background(), ws.ID, "procedural")
	require.NoError(t, err)
	assert.Len(t, procedural, 1)
	assert.Equal(t, "review", procedural[0].Origin)
}

func TestSumWorkspaceTokensSince(t *testing.T) {
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, store.Migrate(context.Background(), db))

	ctx := context.Background()

	ws := &store.Workspace{Name: "sumtok", Path: t.TempDir(), ConfigHash: "h"}
	require.NoError(t, db.UpsertWorkspace(ctx, ws))

	task := &store.Task{WorkspaceID: ws.ID, SignalType: "git.commit", Summary: "x"}
	require.NoError(t, db.CreateTask(ctx, task))

	require.NoError(t, db.AppendTaskEvent(ctx, task.ID, "usage", map[string]any{"total": 1000}))
	require.NoError(t, db.AppendTaskEvent(ctx, task.ID, "usage", map[string]any{"total": 500}))
	// Non-usage events must be ignored.
	require.NoError(t, db.AppendTaskEvent(ctx, task.ID, "triage", map[string]any{"total": 9999}))

	sum, err := db.SumWorkspaceTokensSince(ctx, ws.ID, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1500, sum)

	// A future "since" excludes everything.
	sum, err = db.SumWorkspaceTokensSince(ctx, ws.ID, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 0, sum)
}
