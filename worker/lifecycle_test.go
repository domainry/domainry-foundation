package worker

import (
	"context"
	"testing"
	"time"
)

func TestStartWorkerLoopDoesNotTickCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := make(chan struct{}, 1)
	StartNamedLoop(ctx, Identity{Owner: "test", Name: "cancelled"}, time.Millisecond, func(context.Context) (TickResult, error) {
		called <- struct{}{}
		return TickResult{}, nil
	})
	select {
	case <-called:
		t.Fatal("cancelled worker executed a repository tick")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestStartWorkerLoopStopsSchedulingAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 2)
	StartNamedLoop(ctx, Identity{Owner: "test", Name: "cancel"}, time.Hour, func(context.Context) (TickResult, error) {
		called <- struct{}{}
		return TickResult{}, nil
	})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("worker did not execute initial tick")
	}
	cancel()
	select {
	case <-called:
		t.Fatal("worker scheduled another tick after cancellation")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestStartWorkerLoopDoneWaitsForActiveTick(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	release := make(chan struct{})
	done := StartNamedLoop(ctx, Identity{Owner: "test", Name: "active"}, time.Hour, func(context.Context) (TickResult, error) {
		close(started)
		<-release
		return TickResult{}, nil
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("worker reported stopped while active tick was still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not report stopped after active tick returned")
	}
}

func TestStartNamedLoopRecoversPanicAndContinues(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	continued := make(chan struct{})
	ticks := 0
	done := StartNamedLoop(ctx, Identity{Owner: "test", Name: "panic-test"}, time.Millisecond, func(context.Context) (TickResult, error) {
		ticks++
		if ticks == 1 {
			panic("broken tick")
		}
		close(continued)
		cancel()
		return TickResult{}, nil
	})
	select {
	case <-continued:
	case <-time.After(time.Second):
		t.Fatal("worker did not continue after recovered panic")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

func TestStartNamedLoopHandlesNilTickAndInvalidInterval(t *testing.T) {
	select {
	case <-StartNamedLoop(t.Context(), Identity{Owner: "test", Name: "nil"}, 0, nil):
	case <-time.After(time.Second):
		t.Fatal("nil worker tick did not stop")
	}
}

func TestStartWakeableRecoveryLoopRunsWakeupsAndRecoversLostSignals(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wakeups := make(chan string, 1)
	recovered, executed := make(chan struct{}, 4), make(chan string, 1)
	done := StartWakeableRecoveryLoop(ctx, Identity{Owner: "agent", Name: "agent_task"}, 10*time.Millisecond, wakeups, func(context.Context) (TickResult, error) {
		recovered <- struct{}{}
		return TickResult{}, nil
	}, func(_ context.Context, value string) (TickResult, error) {
		executed <- value
		return TickResult{Worked: true, ProcessedCount: 1}, nil
	})
	wakeups <- "workspace/run"
	select {
	case got := <-executed:
		if got != "workspace/run" {
			t.Fatalf("executed=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("committed task wakeup was not executed")
	}
	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("recovery pass did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wakeable loop did not stop")
	}
}

func TestAdaptiveLoopIntervalBacksOffAndResets(t *testing.T) {
	minimum, maximum := time.Second, 5*time.Second
	interval := nextAdaptiveInterval(minimum, minimum, maximum, false)
	if interval != 2*time.Second {
		t.Fatalf("first idle interval=%s", interval)
	}
	interval = nextAdaptiveInterval(interval, minimum, maximum, false)
	if interval != 4*time.Second {
		t.Fatalf("second idle interval=%s", interval)
	}
	interval = nextAdaptiveInterval(interval, minimum, maximum, false)
	if interval != maximum {
		t.Fatalf("capped idle interval=%s", interval)
	}
	if interval = nextAdaptiveInterval(interval, minimum, maximum, true); interval != minimum {
		t.Fatalf("worked interval=%s", interval)
	}
}

func TestWakeableAdaptiveLoopInterruptsIdleBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wakeups := make(chan string, 1)
	ticks := make(chan time.Time, 2)
	done := StartWakeableAdaptiveLoop(ctx, Identity{Owner: "runtime", Name: "record_batch"}, time.Hour, time.Hour, wakeups, func(context.Context) (TickResult, error) {
		ticks <- time.Now()
		return TickResult{}, nil
	})
	select {
	case <-ticks:
	case <-time.After(time.Second):
		t.Fatal("adaptive loop did not run its initial recovery")
	}
	wakeups <- "job-1"
	select {
	case <-ticks:
	case <-time.After(time.Second):
		t.Fatal("wakeup did not interrupt idle backoff")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wakeable adaptive loop did not stop")
	}
}

func TestJoinWaitsForEveryWorker(t *testing.T) {
	first, second := make(chan struct{}), make(chan struct{})
	done := Join(first, nil, second)
	close(first)
	select {
	case <-done:
		t.Fatal("joined lifecycle stopped before every worker")
	default:
	}
	close(second)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("joined lifecycle did not stop")
	}
}
