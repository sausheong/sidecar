package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/sidecar/internal/store"
)

const reflectSystemPrompt = `You are a memory extraction agent for an autonomous software engineering system.
Given a summary of a completed engineering task, extract useful knowledge.

Respond with ONLY valid JSON in this exact format:
{
  "episodic": "one sentence: what was done and the outcome",
  "semantic": ["architectural insight 1"],
  "procedural": ["specific command or workflow that worked"],
  "policies": []
}

Guidelines:
- episodic: always provide a factual one-sentence summary (never omit)
- semantic: code patterns, conventions, fragile areas discovered (may be empty array)
- procedural: specific commands or steps confirmed to work, e.g. "run 'make test' not 'go test ./...'" (may be empty array)
- policies: constraints learned from PR reviewer feedback (usually empty)
- Keep each entry to one concise sentence
- Respond with JSON only — no prose`

// ReflectResponse is the structured output of the reflect agent.
type ReflectResponse struct {
	Episodic   string   `json:"episodic"`
	Semantic   []string `json:"semantic"`
	Procedural []string `json:"procedural"`
	Policies   []string `json:"policies"`
}

// ParseReflectResponse parses the reflect agent's JSON response.
// Exported for testing.
func ParseReflectResponse(raw string) (ReflectResponse, error) {
	raw = strings.TrimSpace(raw)
	var resp ReflectResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return ReflectResponse{}, fmt.Errorf("parsing reflect JSON: %w", err)
	}
	return resp, nil
}

// BuildReflectMessage constructs the user message for the reflect agent.
// Exported for testing.
func BuildReflectMessage(task *store.Task, events []*store.TaskEvent) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Signal: %s\nSummary: %s\nStatus: %s\n",
		task.SignalType, task.Summary, task.Status))
	for _, ev := range events {
		if ev.Type == "triage" || ev.Type == "pr_created" || ev.Type == "suggestion" {
			data, _ := json.Marshal(ev.Payload)
			sb.WriteString(fmt.Sprintf("Event [%s]: %s\n", ev.Type, string(data)))
		}
	}
	sb.WriteString("\nExtract memory from this completed task.")
	return sb.String()
}

// Reflect extracts insights from a completed task and writes them to memory.
// Runs asynchronously — caller should launch in a goroutine with a timeout context.
func Reflect(
	ctx context.Context,
	provider EmbeddingProvider,
	llmProvider llm.LLMProvider,
	triageModel string,
	db *store.DB,
	workspace *store.Workspace,
	task *store.Task,
	events []*store.TaskEvent,
) error {
	msg := BuildReflectMessage(task, events)

	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: llmProvider,
			Tools:    tool.NewRegistry(),
			Session:  session.NewSession("reflect-"+uuid.New().String(), "main"),
		},
		runtime.AgentSpec{
			ID:           "reflect",
			Name:         "Reflect",
			Model:        triageModel,
			SystemPrompt: reflectSystemPrompt,
			MaxTurns:     1,
		},
	)
	if err != nil {
		slog.Warn("reflect: runtime build failed, storing episodic only", "err", err)
		return storeEpisodicFallback(ctx, provider, db, workspace, task)
	}
	defer rt.Close()

	evts, err := rt.Run(ctx, msg, nil)
	if err != nil {
		slog.Warn("reflect: run failed, storing episodic only", "err", err)
		return storeEpisodicFallback(ctx, provider, db, workspace, task)
	}

	var sb strings.Builder
	for ev := range evts {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
	}

	resp, err := ParseReflectResponse(sb.String())
	if err != nil {
		slog.Warn("reflect: parse failed, storing episodic only", "err", err, "raw", sb.String())
		return storeEpisodicFallback(ctx, provider, db, workspace, task)
	}

	return storeReflectResponse(ctx, provider, db, workspace, resp)
}

func storeReflectResponse(ctx context.Context, provider EmbeddingProvider, db *store.DB, workspace *store.Workspace, resp ReflectResponse) error {
	// Batch all text entries that need embedding.
	var texts, categories []string
	if resp.Episodic != "" {
		texts = append(texts, resp.Episodic)
		categories = append(categories, "episodic")
	}
	for _, s := range resp.Semantic {
		texts = append(texts, s)
		categories = append(categories, "semantic")
	}
	for _, p := range resp.Procedural {
		texts = append(texts, p)
		categories = append(categories, "procedural")
	}

	if len(texts) > 0 {
		embeddings, err := provider.Embed(ctx, texts, "document")
		if err != nil {
			return fmt.Errorf("embedding reflect entries: %w", err)
		}
		for i, content := range texts {
			if i < len(embeddings) {
				if err := db.StoreMemory(ctx, workspace.ID, categories[i], content, embeddings[i]); err != nil {
					slog.Warn("reflect: failed to store memory entry", "category", categories[i], "err", err)
				}
			}
		}
	}

	for _, p := range resp.Policies {
		if err := db.StorePolicy(ctx, workspace.ID, p, "learned"); err != nil {
			slog.Warn("reflect: failed to store policy", "err", err)
		}
	}
	return nil
}

// storeEpisodicFallback writes a deterministic episodic entry when the LLM call fails.
func storeEpisodicFallback(ctx context.Context, provider EmbeddingProvider, db *store.DB, workspace *store.Workspace, task *store.Task) error {
	content := fmt.Sprintf("%s: %s → %s", task.SignalType, task.Summary, task.Status)
	embeddings, err := provider.Embed(ctx, []string{content}, "document")
	if err != nil {
		return fmt.Errorf("embedding episodic fallback: %w", err)
	}
	if len(embeddings) == 0 {
		return nil
	}
	return db.StoreMemory(ctx, workspace.ID, "episodic", content, embeddings[0])
}
