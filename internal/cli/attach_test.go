package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestAttachCmd_RequiresYAML(t *testing.T) {
	root := cli.RootCmd()
	root.SetArgs([]string{"attach", "/nonexistent/path"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sidecar.yaml")
}
