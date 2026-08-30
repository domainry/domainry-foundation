package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatcherWithExecutorsBoundsScanAndExecutionCapacity(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var inFlight, maximum, processed atomic.Int32
	release := make(chan struct{})
	done := StartDispatcherWithExecutors(ctx, DispatcherExecutorConfig{Concurrency: 3, MinInterval: time.Millisecond, MaxInterval: 5 * time.Millisecond},
		func(_ context.Context, available int) ([]int, error) {
			if available > 3 {
				t.Fatalf("available=%d exceeds capacity", available)
			}
			items := make([]int, available)
			return items, nil
		},
		func(context.Context, int) {
			current := inFlight.Add(1)
			for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
			}
			<-release
			inFlight.Add(-1)
			processed.Add(1)
		},
	)
	deadline := time.Now().Add(time.Second)
	for maximum.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if maximum.Load() != 3 {
		t.Fatalf("maximum in flight=%d", maximum.Load())
	}
	for range 3 {
		release <- struct{}{}
	}
	deadline = time.Now().Add(time.Second)
	for processed.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	close(release)
	<-done
}

func TestWakeableDispatcherExecutesLocatorsAndRecoversLostWakeups(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wakeups := make(chan int, 1)
	executed := make(chan int, 4)
	var recoveries atomic.Int32
	done := StartWakeableDispatcherWithExecutors(ctx, DispatcherExecutorConfig{Name: "outbox", Concurrency: 2}, 10*time.Millisecond, wakeups,
		func(context.Context, int) ([]int, error) {
			if recoveries.Add(1) == 2 {
				return []int{2}, nil
			}
			return nil, nil
		},
		func(_ context.Context, value int) { executed <- value },
	)
	wakeups <- 1
	seen := map[int]bool{}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(seen) < 2 {
		select {
		case value := <-executed:
			seen[value] = true
		case <-deadline.C:
			t.Fatalf("executed=%#v recoveries=%d", seen, recoveries.Load())
		}
	}
	cancel()
	<-done
}
