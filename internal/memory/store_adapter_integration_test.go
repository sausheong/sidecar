//go:build integration

package memory_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	harnessmem "github.com/sausheong/harness/tool/memory"
	"github.com/sausheong/sidecar/internal/memory"
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

// fakeEmbedder returns a fixed 1024-dim vector regardless of input.
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, texts []string, _ string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, 1024)
		out[i][0] = 0.5
	}
	return out, nil
}

func (fakeEmbedder) Dims() int { return 1024 }

func newAdapter(t *testing.T) (*memory.HarnessStoreAdapter, *store.DB, uuid.UUID) {
	t.Helper()
	db, err := store.Connect(context.Background(), dbURL(t))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, store.Migrate(context.Background(), db))

	ws := &store.Workspace{Name: "adapter-test", Path: t.TempDir(), ConfigHash: "x"}
	require.NoError(t, db.UpsertWorkspace(context.Background(), ws))

	return memory.NewHarnessStoreAdapter(db, fakeEmbedder{}, ws.ID), db, ws.ID
}

func TestAdapter_Save_DefaultsAndFields(t *testing.T) {
	a, _, _ := newAdapter(t)

	saved, err := a.Save(context.Background(), harnessmem.Entry{
		Content: "auth uses interface mocking",
		Tags:    []string{"semantic"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, saved.ID)
	assert.Equal(t, "auth uses interface mocking", saved.Content)
	assert.Equal(t, "agent", saved.Origin)
	assert.False(t, saved.CreatedAt.IsZero())
}

func TestAdapter_Save_OriginFromCtx(t *testing.T) {
	a, _, _ := newAdapter(t)
	ctx := context.WithValue(context.Background(), harnessmem.OriginKey, "review")

	saved, err := a.Save(ctx, harnessmem.Entry{Content: "x", Tags: []string{"episodic"}})
	require.NoError(t, err)
	assert.Equal(t, "review", saved.Origin)
}

func TestAdapter_Save_EmptyContentRejected(t *testing.T) {
	a, _, _ := newAdapter(t)
	_, err := a.Save(context.Background(), harnessmem.Entry{Content: ""})
	assert.ErrorIs(t, err, harnessmem.ErrInvalidContent)
}
