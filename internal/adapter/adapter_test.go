package adapter_test

import (
	"testing"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/stretchr/testify/assert"
)

func TestSignalTypes(t *testing.T) {
	assert.Equal(t, adapter.SignalType("git.commit"), adapter.SignalGitCommit)
	assert.Equal(t, adapter.SignalType("schedule.tick"), adapter.SignalScheduleTick)
	assert.Equal(t, adapter.SignalType("ondemand.task"), adapter.SignalOnDemand)
}
