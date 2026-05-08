package schedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/adapter/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleAdapter_Fires(t *testing.T) {
	// 6-field cron with seconds: fires every second
	a, err := schedule.New("* * * * * *")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	signals := make(chan adapter.Signal, 5)
	require.NoError(t, a.Start(ctx, signals))
	defer a.Stop()

	select {
	case sig := <-signals:
		assert.Equal(t, adapter.SignalScheduleTick, sig.Type)
		assert.Equal(t, "schedule", sig.Source)
	case <-ctx.Done():
		t.Fatal("no signal received within timeout")
	}
}

func TestScheduleAdapter_InvalidCron(t *testing.T) {
	_, err := schedule.New("not-a-cron")
	assert.Error(t, err)
}
