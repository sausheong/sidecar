package cli_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/cli"
	"github.com/stretchr/testify/assert"
)

func TestAskCmd_RequiresDBURL(t *testing.T) {
	t.Setenv("SIDECAR_DB_URL", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"ask", "how does auth work?"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SIDECAR_DB_URL")
}

func TestAskCmd_RequiresAnthropicKey(t *testing.T) {
	t.Setenv("SIDECAR_DB_URL", "postgres://localhost/sidecar")
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := cli.RootCmd()
	root.SetArgs([]string{"ask", "how does auth work?"})
	err := root.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY")
}
