package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestStatusCmd_RequiresDBURL(t *testing.T) {
	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"status"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}
