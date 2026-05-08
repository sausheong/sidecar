package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestTaskCmd_RequiresDescription(t *testing.T) {
	root := cli.RootCmd()
	root.SetArgs([]string{"task"})
	err := root.Execute()
	assert.Error(t, err)
}

func TestTaskCmd_RequiresDBURL(t *testing.T) {
	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"task", "fix the tests", "--repo", "/nonexistent"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}
