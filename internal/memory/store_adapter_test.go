package memory

import (
	"context"
	"testing"

	harnessmem "github.com/sausheong/harness/tool/memory"
	"github.com/stretchr/testify/assert"
)

func TestCategoryFromTags(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, "semantic"},
		{"semantic", []string{"semantic"}, "semantic"},
		{"unknown only", []string{"unknown"}, "semantic"},
		{"unknown then known", []string{"go", "semantic"}, "semantic"},
		{"episodic", []string{"episodic", "extra"}, "episodic"},
		{"procedural", []string{"procedural"}, "procedural"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, categoryFromTags(c.in))
		})
	}
}

func TestOriginFromCtx(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"no key", context.Background(), "agent"},
		{"empty string", context.WithValue(context.Background(), harnessmem.OriginKey, ""), "agent"},
		{"review", context.WithValue(context.Background(), harnessmem.OriginKey, "review"), "review"},
		{"custom", context.WithValue(context.Background(), harnessmem.OriginKey, "test"), "test"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, originFromCtx(c.ctx))
		})
	}
}
