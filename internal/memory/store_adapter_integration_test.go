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

func TestAdapter_Get_RoundTripAndNotFound(t *testing.T) {
	a, _, _ := newAdapter(t)

	saved, err := a.Save(context.Background(), harnessmem.Entry{
		Content: "x", Tags: []string{"semantic"},
	})
	require.NoError(t, err)

	got, ok, err := a.Get(context.Background(), saved.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "x", got.Content)
	assert.Equal(t, []string{"semantic"}, got.Tags)

	_, ok, err = a.Get(context.Background(), uuid.New().String())
	require.NoError(t, err)
	assert.False(t, ok)

	_, ok, err = a.Get(context.Background(), "not-a-uuid")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestAdapter_List_AllAndByTag(t *testing.T) {
	a, _, _ := newAdapter(t)

	_, err := a.Save(context.Background(), harnessmem.Entry{Content: "a", Tags: []string{"semantic"}})
	require.NoError(t, err)
	_, err = a.Save(context.Background(), harnessmem.Entry{Content: "b", Tags: []string{"procedural"}})
	require.NoError(t, err)

	all, err := a.List(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	semantic, err := a.List(context.Background(), "semantic")
	require.NoError(t, err)
	require.Len(t, semantic, 1)
	assert.Equal(t, "a", semantic[0].Content)
}

func TestAdapter_Remove_Idempotent(t *testing.T) {
	a, _, _ := newAdapter(t)

	saved, err := a.Save(context.Background(), harnessmem.Entry{Content: "x", Tags: []string{"semantic"}})
	require.NoError(t, err)

	require.NoError(t, a.Remove(context.Background(), saved.ID))
	_, ok, err := a.Get(context.Background(), saved.ID)
	require.NoError(t, err)
	assert.False(t, ok)

	// Idempotent
	require.NoError(t, a.Remove(context.Background(), saved.ID))
	require.NoError(t, a.Remove(context.Background(), "not-a-uuid"))
}

func TestAdapter_Update_InheritsCategoryAndOrigin(t *testing.T) {
	a, _, _ := newAdapter(t)
	ctx := context.WithValue(context.Background(), harnessmem.OriginKey, "review")

	saved, err := a.Save(ctx, harnessmem.Entry{
		Content: "old",
		Tags:    []string{"procedural"},
	})
	require.NoError(t, err)

	updated, err := a.Update(context.Background(), saved.ID, "new")
	require.NoError(t, err)
	assert.NotEqual(t, saved.ID, updated.ID, "Update returns a fresh ID")
	assert.Equal(t, "new", updated.Content)
	assert.Equal(t, []string{"procedural"}, updated.Tags, "category inherited")
	assert.Equal(t, "review", updated.Origin, "origin inherited from old row")

	// Old id no longer resolves
	_, ok, err := a.Get(context.Background(), saved.ID)
	require.NoError(t, err)
	assert.False(t, ok, "old id is invalidated")

	// New id resolves
	_, ok, err = a.Get(context.Background(), updated.ID)
	require.NoError(t, err)
	assert.True(t, ok, "new id resolves")
}

func TestAdapter_Update_NotFound(t *testing.T) {
	a, _, _ := newAdapter(t)

	_, err := a.Update(context.Background(), uuid.New().String(), "x")
	assert.ErrorIs(t, err, harnessmem.ErrNotFound)

	_, err = a.Update(context.Background(), "not-a-uuid", "x")
	assert.ErrorIs(t, err, harnessmem.ErrNotFound)
}

func TestAdapter_Update_EmptyContentRejected(t *testing.T) {
	a, _, _ := newAdapter(t)

	saved, err := a.Save(context.Background(), harnessmem.Entry{Content: "x", Tags: []string{"semantic"}})
	require.NoError(t, err)

	_, err = a.Update(context.Background(), saved.ID, "")
	assert.ErrorIs(t, err, harnessmem.ErrInvalidContent)
}
