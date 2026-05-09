package memory

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	harnessmem "github.com/sausheong/harness/tool/memory"
	"github.com/sausheong/sidecar/internal/store"
)

// HarnessStoreAdapter implements harnessmem.MemoryStore over the
// pgvector-backed *store.DB. It is bound to one workspace at
// construction; cross-workspace operations require a separate adapter.
type HarnessStoreAdapter struct {
	db          *store.DB
	embeddings  EmbeddingProvider
	workspaceID uuid.UUID
}

// NewHarnessStoreAdapter constructs an adapter bound to one workspace.
// embeddings must be non-nil; if Embed returns an error, Save fails
// without writing the row.
func NewHarnessStoreAdapter(db *store.DB, embeddings EmbeddingProvider, workspaceID uuid.UUID) *HarnessStoreAdapter {
	return &HarnessStoreAdapter{db: db, embeddings: embeddings, workspaceID: workspaceID}
}

// Save embeds the entry's Content and inserts a row.
//   - Category derives from the first recognized category tag
//     ("episodic" | "semantic" | "procedural"), defaulting to "semantic".
//   - Origin is read from ctx via harnessmem.OriginKey, defaulting to
//     "agent".
//   - Returned Entry has ID set to the row's UUID.
func (a *HarnessStoreAdapter) Save(ctx context.Context, e harnessmem.Entry) (harnessmem.Entry, error) {
	if e.Content == "" {
		return harnessmem.Entry{}, harnessmem.ErrInvalidContent
	}
	category := categoryFromTags(e.Tags)
	origin := originFromCtx(ctx)

	embeds, err := a.embeddings.Embed(ctx, []string{e.Content}, "document")
	if err != nil {
		return harnessmem.Entry{}, fmt.Errorf("embed: %w", err)
	}
	if len(embeds) == 0 {
		return harnessmem.Entry{}, fmt.Errorf("embed: empty result")
	}

	id, createdAt, err := a.db.StoreMemoryReturning(ctx, a.workspaceID, category, e.Content, origin, embeds[0])
	if err != nil {
		return harnessmem.Entry{}, fmt.Errorf("store: %w", err)
	}

	return harnessmem.Entry{
		ID:        id.String(),
		Content:   e.Content,
		Tags:      e.Tags,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Origin:    origin,
	}, nil
}

// categoryFromTags returns the first recognized category tag, or
// "semantic" when none of the entry's tags match. Recognized values:
// episodic, semantic, procedural.
func categoryFromTags(tags []string) string {
	for _, t := range tags {
		switch t {
		case "episodic", "semantic", "procedural":
			return t
		}
	}
	return "semantic"
}

// originFromCtx reads harnessmem.OriginKey from ctx, defaulting to
// "agent" when the key is unset or the value is empty.
func originFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(harnessmem.OriginKey).(string); ok && v != "" {
		return v
	}
	return "agent"
}
