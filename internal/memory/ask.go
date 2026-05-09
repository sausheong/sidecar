package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/sausheong/harness/llm"
	"github.com/sausheong/harness/runtime"
	"github.com/sausheong/harness/session"
	"github.com/sausheong/harness/tool"
	"github.com/sausheong/sidecar/internal/store"
)

const askLimit = 8

const askSystemPrompt = `You are a knowledgeable assistant for a software project. You have access to
the project's accumulated memory — facts, patterns, and history extracted from real engineering work.

Answer the developer's question using ONLY the information provided in the memory below.
Be concise, direct, and accurate. If the memory doesn't contain enough information to
answer confidently, say so explicitly rather than guessing.`

// Ask retrieves workspace memory relevant to the query and synthesises a
// natural-language answer using a Haiku agent.
// If no memory exists for the workspace, the agent is informed and will say so.
func Ask(
	ctx context.Context,
	provider EmbeddingProvider,
	llmProvider llm.LLMProvider,
	model string,
	db *store.DB,
	workspace *store.Workspace,
	query string,
) (string, error) {
	// Embed the query.
	embeddings, err := provider.Embed(ctx, []string{query}, "query")
	if err != nil {
		return "", fmt.Errorf("embedding query: %w", err)
	}
	if len(embeddings) == 0 || len(embeddings[0]) == 0 {
		return "", fmt.Errorf("embedding returned empty result")
	}

	// Search all three memory categories (ask includes episodic unlike Retrieve).
	results, err := db.SearchMemory(ctx, workspace.ID,
		[]string{"semantic", "procedural", "episodic"}, embeddings[0], askLimit)
	if err != nil {
		return "", fmt.Errorf("searching memory: %w", err)
	}

	policies, err := db.GetPolicies(ctx, workspace.ID)
	if err != nil {
		return "", fmt.Errorf("loading policies: %w", err)
	}

	msg := BuildAskMessage(results, policies, query)

	// Synthesise answer with a single Haiku turn.
	rt, err := runtime.BuildRuntime(
		runtime.RuntimeDeps{},
		runtime.RuntimeInputs{
			Provider: llmProvider,
			Tools:    tool.NewRegistry(),
			Session:  session.NewSession("ask-"+uuid.New().String(), "main"),
		},
		runtime.AgentSpec{
			ID:           "ask",
			Name:         "Ask",
			Model:        model,
			SystemPrompt: askSystemPrompt,
			MaxTurns:     1,
		},
	)
	if err != nil {
		return "", fmt.Errorf("building ask runtime: %w", err)
	}
	defer rt.Close()

	events, err := rt.Run(ctx, msg, nil)
	if err != nil {
		return "", fmt.Errorf("ask run: %w", err)
	}

	var sb strings.Builder
	for ev := range events {
		if ev.Type == runtime.EventTextDelta {
			sb.WriteString(ev.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

// BuildAskMessage formats the memory context and question for the synthesis agent.
// Exported for testing.
func BuildAskMessage(results []*store.MemorySearchResult, policies []string, query string) string {
	var sb strings.Builder

	if len(results) == 0 && len(policies) == 0 {
		sb.WriteString("Memory: no memory available for this workspace.\n\n")
		sb.WriteString("Question: " + query)
		return sb.String()
	}

	sb.WriteString("Memory:\n")

	// Group by category, include all above threshold.
	var semantic, procedural, episodic []string
	for _, r := range results {
		if r.Similarity < similarityThreshold {
			continue
		}
		switch r.Category {
		case "semantic":
			semantic = append(semantic, r.Content)
		case "procedural":
			procedural = append(procedural, r.Content)
		case "episodic":
			episodic = append(episodic, r.Content)
		}
	}

	if len(semantic) == 0 && len(procedural) == 0 && len(episodic) == 0 && len(policies) == 0 {
		sb.Reset()
		sb.WriteString("Memory: no memory available for this workspace.\n\n")
		sb.WriteString("Question: " + query)
		return sb.String()
	}

	if len(semantic) > 0 {
		sb.WriteString("\nArchitecture:\n")
		for _, s := range semantic {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(procedural) > 0 {
		sb.WriteString("\nWorkflows:\n")
		for _, s := range procedural {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(episodic) > 0 {
		sb.WriteString("\nHistory:\n")
		for _, s := range episodic {
			sb.WriteString("- " + s + "\n")
		}
	}
	if len(policies) > 0 {
		sb.WriteString("\nPolicies:\n")
		for _, p := range policies {
			sb.WriteString("- " + p + "\n")
		}
	}

	sb.WriteString("\nQuestion: " + query)
	return sb.String()
}
