package daemon_test

import (
	"context"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAdapter struct {
	name    string
	signals []adapter.Signal
}

func (f *fakeAdapter) Name() string { return f.name }
func (f *fakeAdapter) Stop() error  { return nil }
func (f *fakeAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	go func() {
		for _, sig := range f.signals {
			select {
			case <-ctx.Done():
				return
			case out <- sig:
			}
		}
	}()
	return nil
}

func TestDaemon_RoutesSignals(t *testing.T) {
	received := make([]adapter.Signal, 0)
	handler := func(ctx context.Context, sig adapter.Signal) error {
		received = append(received, sig)
		return nil
	}

	adapters := []adapter.Adapter{
		&fakeAdapter{name: "fake", signals: []adapter.Signal{
			{Type: adapter.SignalGitCommit, Source: "fake"},
			{Type: adapter.SignalScheduleTick, Source: "fake"},
		}},
	}

	d := daemon.New(adapters, handler)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	require.NoError(t, d.Start(ctx))
	time.Sleep(200 * time.Millisecond)
	d.Stop()

	assert.Len(t, received, 2)
	assert.Equal(t, adapter.SignalGitCommit, received[0].Type)
}
