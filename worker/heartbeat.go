package worker

import (
	"context"
	"sync"
	"time"
)

// workerHeartbeat keeps a database lease alive while user or connector code is
// running. stop is idempotent and reports a lost lease to the caller before it
// attempts the terminal CAS update.
func WithHeartbeat(parent context.Context, interval time.Duration, heartbeat func(context.Context) error) (context.Context, func() error) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				if err := heartbeat(ctx); err != nil {
					done <- err
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	var result error
	return ctx, func() error { once.Do(func() { cancel(); result = <-done }); return result }
}
