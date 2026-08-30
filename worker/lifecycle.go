package worker

import (
	"context"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	"go.uber.org/zap"
)

func StartLoop(ctx context.Context, interval time.Duration, tick func()) <-chan struct{} {
	return StartNamedLoop(ctx, "worker", interval, tick)
}

// StartNamedLoop runs a recurring task until cancellation. A panic in one tick
// is isolated to that tick, recorded with its worker identity and stack, and
// does not terminate either the loop or its hosting process.
func StartNamedLoop(ctx context.Context, name string, interval time.Duration, tick func()) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil || tick == nil {
		close(done)
		return done
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "worker"
	}
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		default:
			runTick(ctx, name, tick)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				runTick(ctx, name, tick)
			}
		}
	}()
	return done
}

// StartAdaptiveLoop polls quickly while work is available and backs off while
// idle. Durable queue ownership and retries remain the tick's responsibility.
func StartAdaptiveLoop(ctx context.Context, name string, minInterval, maxInterval time.Duration, tick func() bool) <-chan struct{} {
	return startWakeableAdaptiveLoop[struct{}](ctx, name, minInterval, maxInterval, nil, tick)
}

// StartWakeableAdaptiveLoop preserves adaptive durable recovery while allowing
// committed local work to interrupt the idle backoff. Wakeups are lossy hints;
// tick must still claim durable work from its owner store.
func StartWakeableAdaptiveLoop[T any](ctx context.Context, name string, minInterval, maxInterval time.Duration, wakeups <-chan T, tick func() bool) <-chan struct{} {
	return startWakeableAdaptiveLoop(ctx, name, minInterval, maxInterval, wakeups, tick)
}

func startWakeableAdaptiveLoop[T any](ctx context.Context, name string, minInterval, maxInterval time.Duration, wakeups <-chan T, tick func() bool) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil || tick == nil {
		close(done)
		return done
	}
	if minInterval <= 0 {
		minInterval = time.Second
	}
	if maxInterval < minInterval {
		maxInterval = minInterval
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "worker"
	}
	go func() {
		defer close(done)
		interval := minInterval
		for {
			worked := false
			runTick(ctx, name, func() { worked = tick() })
			if ctx.Err() != nil {
				return
			}
			interval = nextAdaptiveInterval(interval, minInterval, maxInterval, worked)
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return
			case _, ok := <-wakeups:
				if !timer.Stop() {
					<-timer.C
				}
				if !ok {
					wakeups = nil
				}
			case <-timer.C:
			}
		}
	}()
	return done
}

func nextAdaptiveInterval(current, minInterval, maxInterval time.Duration, worked bool) time.Duration {
	if worked {
		return minInterval
	}
	if current >= maxInterval {
		return maxInterval
	}
	current *= 2
	if current > maxInterval {
		return maxInterval
	}
	return current
}

// StartWakeableRecoveryLoop executes committed task locators immediately while
// retaining one periodic durable recovery pass for lost wakeups, retries,
// expired leases and process restarts. Wakeups are hints; ownership stays with
// the task owner's database claim.
func StartWakeableRecoveryLoop[T any](ctx context.Context, name string, recoveryInterval time.Duration, wakeups <-chan T, recover func(), execute func(T)) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil || recover == nil || execute == nil {
		close(done)
		return done
	}
	if recoveryInterval <= 0 {
		recoveryInterval = 30 * time.Second
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "worker"
	}
	go func() {
		defer close(done)
		runTick(ctx, name, recover)
		timer := time.NewTimer(recoveryInterval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case value, ok := <-wakeups:
				if !ok {
					wakeups = nil
					continue
				}
				runTick(ctx, name, func() { execute(value) })
			case <-timer.C:
				runTick(ctx, name, recover)
				timer.Reset(recoveryInterval)
			}
		}
	}()
	return done
}

func runTick(ctx context.Context, name string, tick func()) {
	started := time.Now()
	owner, observed := defaultOperationalMetrics.begin(name)
	outcome := "success"
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = "panic"
			logging.FromContext(ctx).Error("worker tick panicked",
				zap.String("worker", name),
				zap.Any("panic", recovered),
				zap.ByteString("stack", debug.Stack()),
			)
		}
		if observed {
			defaultOperationalMetrics.end(owner, outcome, time.Since(started))
		}
	}()
	tick()
}

func Stopped() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func Join(doneChannels ...<-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var workers sync.WaitGroup
		for _, channel := range doneChannels {
			if channel == nil {
				continue
			}
			workers.Add(1)
			go func(ch <-chan struct{}) { defer workers.Done(); <-ch }(channel)
		}
		workers.Wait()
	}()
	return done
}

type DispatcherExecutorConfig struct {
	Name        string
	Concurrency int
	MinInterval time.Duration
	MaxInterval time.Duration
}

// StartDispatcherWithExecutors keeps one queue-discovery goroutine separate
// from a bounded executor pool.
// scan is never asked for more work than the pool can immediately hold.
func StartDispatcherWithExecutors[T any](ctx context.Context, config DispatcherExecutorConfig, scan func(context.Context, int) ([]T, error), execute func(context.Context, T)) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil || scan == nil || execute == nil {
		close(done)
		return done
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 1
	}
	if config.MinInterval <= 0 {
		config.MinInterval = time.Second
	}
	if config.MaxInterval < config.MinInterval {
		config.MaxInterval = config.MinInterval
	}
	jobs := make(chan T, config.Concurrency)
	slots := make(chan struct{}, config.Concurrency)
	wake := make(chan struct{}, 1)
	go func() {
		defer close(done)
		var executors sync.WaitGroup
		for range config.Concurrency {
			executors.Add(1)
			go func() {
				defer executors.Done()
				for job := range jobs {
					if ctx.Err() == nil {
						execute(ctx, job)
					}
					<-slots
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			}()
		}
		interval := config.MinInterval
		for {
			available := config.Concurrency - len(slots)
			if available > 0 {
				items, err := scan(ctx, available)
				if err == nil {
					if len(items) > available {
						items = items[:available]
					}
					for _, item := range items {
						slots <- struct{}{}
						jobs <- item
					}
				}
				interval = nextAdaptiveInterval(interval, config.MinInterval, config.MaxInterval, err == nil && len(items) > 0)
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				close(jobs)
				executors.Wait()
				return
			case <-wake:
				if !timer.Stop() {
					<-timer.C
				}
			case <-timer.C:
			}
		}
	}()
	return done
}

// StartWakeableDispatcherWithExecutors dispatches committed task locators to a
// bounded executor pool and runs a low-frequency durable recovery query. The
// wakeup channel is deliberately lossy; database claim remains the ownership
// boundary and recovery remains the source of eventual progress.
func StartWakeableDispatcherWithExecutors[T any](ctx context.Context, config DispatcherExecutorConfig, recoveryInterval time.Duration, wakeups <-chan T, recover func(context.Context, int) ([]T, error), execute func(context.Context, T)) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil || recover == nil || execute == nil {
		close(done)
		return done
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 1
	}
	if recoveryInterval <= 0 {
		recoveryInterval = 30 * time.Second
	}
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = "worker"
	}
	jobs := make(chan T, config.Concurrency)
	completed := make(chan struct{}, config.Concurrency)
	go func() {
		defer close(done)
		var executors sync.WaitGroup
		for range config.Concurrency {
			executors.Add(1)
			go func() {
				defer executors.Done()
				for job := range jobs {
					if ctx.Err() == nil {
						runTick(ctx, name, func() { execute(ctx, job) })
					}
					completed <- struct{}{}
				}
			}()
		}
		inFlight := 0
		dispatchRecovery := func() {
			available := config.Concurrency - inFlight
			if available <= 0 {
				return
			}
			items, err := recover(ctx, available)
			if err != nil {
				logging.FromContext(ctx).Error("worker recovery failed", zap.String("worker", name), zap.Error(err))
				return
			}
			if len(items) > available {
				items = items[:available]
			}
			for _, item := range items {
				jobs <- item
				inFlight++
			}
		}
		dispatchRecovery()
		timer := time.NewTimer(recoveryInterval)
		defer timer.Stop()
		for {
			input := wakeups
			if inFlight >= config.Concurrency {
				input = nil
			}
			select {
			case <-ctx.Done():
				close(jobs)
				executors.Wait()
				return
			case item, ok := <-input:
				if !ok {
					wakeups = nil
					continue
				}
				jobs <- item
				inFlight++
			case <-completed:
				inFlight--
			case <-timer.C:
				dispatchRecovery()
				timer.Reset(recoveryInterval)
			}
		}
	}()
	return done
}
