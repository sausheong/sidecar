//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"

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
