CREATE TABLE IF NOT EXISTS workspaces (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,
    config_hash TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tasks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    signal_type  TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    summary      TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- task_events records each action taken during a task run.
-- Used by Phase 3 workspace memory to build episodic knowledge.
CREATE TABLE IF NOT EXISTS task_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    UUID NOT NULL REFERENCES tasks(id),
    type       TEXT NOT NULL,
    payload    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Requires pgvector extension (install with: CREATE EXTENSION IF NOT EXISTS vector)
CREATE EXTENSION IF NOT EXISTS vector;

-- Workspace memory entries — embedded and retrieved by semantic similarity.
-- Used by Phase 3 to accumulate architectural knowledge, workflow learnings.
CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    category     TEXT NOT NULL,    -- "episodic" | "semantic" | "procedural"
    content      TEXT NOT NULL,
    embedding    vector(1024),     -- 1024 dims: OpenAI (with dimensions param) + Voyage AI native
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE memory_entries
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS origin     TEXT        NOT NULL DEFAULT 'agent';
CREATE INDEX IF NOT EXISTS memory_entries_embedding_idx
    ON memory_entries USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);
CREATE INDEX IF NOT EXISTS memory_entries_workspace_idx
    ON memory_entries (workspace_id);

-- Policy rules — advisory constraints loaded verbatim into triage/plan context.
CREATE TABLE IF NOT EXISTS policies (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id),
    rule         TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT 'yaml',  -- "yaml" | "learned"
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS policies_workspace_idx
    ON policies (workspace_id);
CREATE UNIQUE INDEX IF NOT EXISTS policies_workspace_rule_unique
    ON policies (workspace_id, rule);
