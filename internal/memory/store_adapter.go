package memory

import (
	"context"
	"errors"
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
		return harnessmem.Entry{}, fmt.Errorf("embedding content: %w", err)
	}
	if len(embeds) == 0 {
		return harnessmem.Entry{}, fmt.Errorf("embedding content: empty result")
	}

	id, createdAt, err := a.db.StoreMemoryReturning(ctx, a.workspaceID, category, e.Content, origin, embeds[0])
	if err != nil {
		return harnessmem.Entry{}, fmt.Errorf("storing memory: %w", err)
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

// Get returns one entry by id. The bool is false (no error) when id
// is malformed or unknown.
func (a *HarnessStoreAdapter) Get(ctx context.Context, id string) (harnessmem.Entry, bool, error) {
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return harnessmem.Entry{}, false, nil
	}
	r, err := a.db.GetMemory(ctx, parsedID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return harnessmem.Entry{}, false, nil
		}
		return harnessmem.Entry{}, false, err
	}
	return harnessmem.Entry{
		ID:        r.ID.String(),
		Content:   r.Content,
		Tags:      []string{r.Category},
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		Origin:    r.Origin,
	}, true, nil
}

// List returns rows for the adapter's workspace ordered by created_at.
// tag filters by category (the tags[0] convention); tag == "" returns
// all rows for the workspace.
func (a *HarnessStoreAdapter) List(ctx context.Context, tag string) ([]harnessmem.Entry, error) {
	rows, err := a.db.ListMemory(ctx, a.workspaceID, tag)
	if err != nil {
		return nil, err
	}
	entries := make([]harnessmem.Entry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, harnessmem.Entry{
			ID:        r.ID.String(),
			Content:   r.Content,
			Tags:      []string{r.Category},
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
			Origin:    r.Origin,
		})
	}
	return entries, nil
}

// Remove deletes the row by id. Idempotent for unknown or malformed ids.
func (a *HarnessStoreAdapter) Remove(ctx context.Context, id string) error {
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil
	}
	return a.db.DeleteMemory(ctx, parsedID)
}

// Update tombstones the old row and inserts a new one with re-embedded
// content. The new entry inherits the old row's category and origin.
// Returns harnessmem.ErrNotFound when id is malformed or unknown.
//
// Best-effort tombstone: the new row is written first, then the old
// row is deleted. If the delete fails, both rows exist briefly; List
// will surface both until the delete succeeds. The brief inconsistency
// is documented in the spec (§10B).
func (a *HarnessStoreAdapter) Update(ctx context.Context, id string, content string) (harnessmem.Entry, error) {
	if content == "" {
		return harnessmem.Entry{}, harnessmem.ErrInvalidContent
	}
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return harnessmem.Entry{}, harnessmem.ErrNotFound
	}

	old, err := a.db.GetMemory(ctx, parsedID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return harnessmem.Entry{}, harnessmem.ErrNotFound
		}
		return harnessmem.Entry{}, fmt.Errorf("get for update: %w", err)
	}

	embeds, err := a.embeddings.Embed(ctx, []string{content}, "document")
	if err != nil {
		return harnessmem.Entry{}, fmt.Errorf("embedding content: %w", err)
	}
	if len(embeds) == 0 {
		return harnessmem.Entry{}, fmt.Errorf("embedding content: empty result")
	}

	newID, createdAt, err := a.db.StoreMemoryReturning(ctx, a.workspaceID, old.Category, content, old.Origin, embeds[0])
	if err != nil {
		return harnessmem.Entry{}, fmt.Errorf("storing memory: %w", err)
	}
	// Best-effort tombstone — failures are accepted (see doc above).
	_ = a.db.DeleteMemory(ctx, parsedID)

	return harnessmem.Entry{
		ID:        newID.String(),
		Content:   content,
		Tags:      []string{old.Category},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
		Origin:    old.Origin,
	}, nil
}

// Compile-time check that the adapter satisfies the harness interface.
var _ harnessmem.MemoryStore = (*HarnessStoreAdapter)(nil)
