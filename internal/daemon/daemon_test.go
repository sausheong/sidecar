package daemon_test

import (
	"context"
	"sync"
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
	var mu sync.Mutex
	received := make([]adapter.Signal, 0)
	var wg sync.WaitGroup
	wg.Add(2)

	handler := func(ctx context.Context, sig adapter.Signal) error {
		mu.Lock()
		received = append(received, sig)
		mu.Unlock()
		wg.Done()
		return nil
	}

	adapters := []adapter.Adapter{
		&fakeAdapter{name: "fake", signals: []adapter.Signal{
			{Type: adapter.SignalGitCommit, Source: "fake"},
			{Type: adapter.SignalScheduleTick, Source: "fake"},
		}},
	}

	d := daemon.New(adapters, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, d.Start(ctx))
	wg.Wait()
	d.Stop()

	assert.Len(t, received, 2)
	assert.Equal(t, adapter.SignalGitCommit, received[0].Type)
}
