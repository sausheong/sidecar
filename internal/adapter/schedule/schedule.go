package schedule

import (
	"context"
	"fmt"

	"github.com/robfig/cron/v3"
	"github.com/sausheong/sidecar/internal/adapter"
)

type ScheduleAdapter struct {
	expr string
	c    *cron.Cron
}

func New(expr string) (*ScheduleAdapter, error) {
	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(expr); err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return &ScheduleAdapter{expr: expr}, nil
}

func (s *ScheduleAdapter) Name() string { return "schedule" }

func (s *ScheduleAdapter) Start(ctx context.Context, out chan<- adapter.Signal) error {
	s.c = cron.New(cron.WithSeconds())
	_, err := s.c.AddFunc(s.expr, func() {
		select {
		case <-ctx.Done():
		case out <- adapter.Signal{
			Type:    adapter.SignalScheduleTick,
			Source:  "schedule",
			Payload: map[string]any{},
		}:
		}
	})
	if err != nil {
		return fmt.Errorf("adding cron job: %w", err)
	}
	s.c.Start()
	return nil
}

func (s *ScheduleAdapter) Stop() error {
	if s.c != nil {
		<-s.c.Stop().Done()
	}
	return nil
}
