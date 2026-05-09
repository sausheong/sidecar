package adapter

import "context"

type SignalType string

const (
	SignalGitCommit    SignalType = "git.commit"
	SignalScheduleTick SignalType = "schedule.tick"
	SignalOnDemand     SignalType = "ondemand.task"
	SignalCIFailure    SignalType = "ci.failure"
	SignalLogAnomaly   SignalType = "log.anomaly"
)

type Signal struct {
	Type    SignalType
	Source  string
	Payload map[string]any
}

type Adapter interface {
	Name() string
	Start(ctx context.Context, out chan<- Signal) error
	Stop() error
}
