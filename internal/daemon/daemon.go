package daemon

import (
	"context"
	"log"

	"github.com/sausheong/sidecar/internal/adapter"
)

type Handler func(ctx context.Context, sig adapter.Signal) error

type Daemon struct {
	adapters []adapter.Adapter
	handler  Handler
	signals  chan adapter.Signal
	done     chan struct{}
}

func New(adapters []adapter.Adapter, handler Handler) *Daemon {
	return &Daemon{
		adapters: adapters,
		handler:  handler,
		signals:  make(chan adapter.Signal, 64),
		done:     make(chan struct{}),
	}
}

func (d *Daemon) Start(ctx context.Context) error {
	for _, a := range d.adapters {
		if err := a.Start(ctx, d.signals); err != nil {
			return err
		}
	}
	go d.run(ctx)
	return nil
}

func (d *Daemon) Stop() {
	for _, a := range d.adapters {
		if err := a.Stop(); err != nil {
			log.Printf("stopping adapter %s: %v", a.Name(), err)
		}
	}
	close(d.done)
}

func (d *Daemon) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.done:
			return
		case sig := <-d.signals:
			if err := d.handler(ctx, sig); err != nil {
				log.Printf("handler error for signal %s: %v", sig.Type, err)
			}
		}
	}
}
