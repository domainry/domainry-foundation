package worker

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	"go.uber.org/zap"
)

// Identity is the bounded process-owned identity of one worker loop.
type Identity struct {
	Owner string
	Name  string
}

// TickResult is the non-payload operational result consumed by the worker
// supervisor. Business results remain in the owner's durable store.
type TickResult struct {
	Worked         bool
	ProcessedCount int
	Attempt        int
}

type Tick func(context.Context) (TickResult, error)

func StartNamedLoop(ctx context.Context, identity Identity, interval time.Duration, tick Tick) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil || tick == nil {
		close(done)
		return done
	}
	identity = normalizedIdentity(identity)
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			return
		default:
			_, _ = observeTick(ctx, identity, "tick", tick)
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
				_, _ = observeTick(ctx, identity, "tick", tick)
			}
		}
	}()
	return done
}

// StartAdaptiveLoop polls quickly while work is available and backs off while
// idle. Durable queue ownership and retries remain the tick's responsibility.
func StartAdaptiveLoop(ctx context.Context, identity Identity, minInterval, maxInterval time.Duration, tick Tick) <-chan struct{} {
	return startWakeableAdaptiveLoop[struct{}](ctx, identity, minInterval, maxInterval, nil, tick)
}

// StartWakeableAdaptiveLoop preserves adaptive durable recovery while allowing
// committed local work to interrupt the idle backoff. Wakeups are lossy hints;
// tick must still claim durable work from its owner store.
func StartWakeableAdaptiveLoop[T any](ctx context.Context, identity Identity, minInterval, maxInterval time.Duration, wakeups <-chan T, tick Tick) <-chan struct{} {
	return startWakeableAdaptiveLoop(ctx, identity, minInterval, maxInterval, wakeups, tick)
}

func startWakeableAdaptiveLoop[T any](ctx context.Context, identity Identity, minInterval, maxInterval time.Duration, wakeups <-chan T, tick Tick) <-chan struct{} {
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
	identity = normalizedIdentity(identity)
	go func() {
		defer close(done)
		interval := minInterval
		for {
			result, _ := observeTick(ctx, identity, "tick", tick)
			if ctx.Err() != nil {
				return
			}
			interval = nextAdaptiveInterval(interval, minInterval, maxInterval, result.Worked)
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
// expired leases and process restarts.
func StartWakeableRecoveryLoop[T any](ctx context.Context, identity Identity, recoveryInterval time.Duration, wakeups <-chan T, recover Tick, execute func(context.Context, T) (TickResult, error)) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil || recover == nil || execute == nil {
		close(done)
		return done
	}
	if recoveryInterval <= 0 {
		recoveryInterval = 30 * time.Second
	}
	identity = normalizedIdentity(identity)
	go func() {
		defer close(done)
		_, _ = observeTick(ctx, identity, "recovery", recover)
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
				_, _ = observeTick(ctx, identity, "execute", func(executeCtx context.Context) (TickResult, error) {
					return execute(executeCtx, value)
				})
			case <-timer.C:
				_, _ = observeTick(ctx, identity, "recovery", recover)
				timer.Reset(recoveryInterval)
			}
		}
	}()
	return done
}

func observeTick(ctx context.Context, identity Identity, operation string, tick Tick) (result TickResult, err error) {
	started := time.Now()
	metricOwner, observed := defaultOperationalMetrics.begin(identity.Owner)
	outcome := "success"
	defer func() {
		duration := time.Since(started)
		if recovered := recover(); recovered != nil {
			outcome = "panic"
			err = apperror.New(apperror.KindInternal, "worker.panic", nil, nil)
			logging.FromContext(ctx).Error("panic_recovered",
				zap.String("component", "worker"),
				zap.String("owner", identity.Owner),
				zap.String("worker", identity.Name),
				zap.String("operation", operation),
				zap.String("panic_type", fmt.Sprintf("%T", recovered)),
				zap.ByteString("stack", debug.Stack()),
				zap.Int64("duration_ms", duration.Milliseconds()),
			)
		} else if err != nil {
			outcome = "error"
			if !cancelledByProcess(ctx, err) {
				err = classifyTickError(identity, operation, err)
				retryable := ClassifyApplicationError(err).Retryable()
				fields := []zap.Field{
					zap.String("owner", identity.Owner),
					zap.String("worker", identity.Name),
					zap.String("operation", operation),
					zap.Int("attempt", max(result.Attempt, 0)),
					zap.Int("processed_count", max(result.ProcessedCount, 0)),
					zap.Int64("duration_ms", duration.Milliseconds()),
				}
				fields = append(fields, logging.ErrorFields(err, logging.Failure{Stage: "worker_" + operation, Retryable: retryable})...)
				logger := logging.FromContext(ctx)
				if retryable {
					logger.Warn("worker_"+operation+"_failed", fields...)
				} else {
					logger.Error("worker_"+operation+"_failed", fields...)
				}
			}
		}
		if observed {
			defaultOperationalMetrics.end(metricOwner, outcome, duration)
		}
	}()
	result, err = tick(ctx)
	if result.ProcessedCount < 0 {
		result.ProcessedCount = 0
	}
	if result.Attempt < 0 {
		result.Attempt = 0
	}
	return result, err
}

func classifyTickError(identity Identity, operation string, err error) error {
	if err == nil || apperror.CodeOf(err) != apperror.DefaultCode(apperror.KindInternal) {
		return err
	}
	code := "backend." + boundedCodeToken(identity.Owner) + "." + boundedCodeToken(identity.Name) + "_" + boundedCodeToken(operation) + "_failed"
	return apperror.New(apperror.KindInternal, code, err, nil)
}

func boundedCodeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			builder.WriteRune(char)
		} else if builder.Len() > 0 {
			builder.WriteByte('_')
		}
	}
	value = strings.Trim(builder.String(), "_")
	if value == "" {
		return "worker"
	}
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

func cancelledByProcess(ctx context.Context, err error) bool {
	if ctx == nil || ctx.Err() == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func normalizedIdentity(identity Identity) Identity {
	identity.Owner = boundedWorkerOwner(identity.Owner)
	identity.Name = strings.ToLower(strings.TrimSpace(identity.Name))
	if identity.Name == "" {
		identity.Name = "worker"
	}
	if identity.Owner == "unknown" {
		identity.Owner = boundedWorkerOwner(identity.Name)
	}
	return identity
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
	Owner       string
	Name        string
	Concurrency int
	MinInterval time.Duration
	MaxInterval time.Duration
}

func (config DispatcherExecutorConfig) identity() Identity {
	return normalizedIdentity(Identity{Owner: config.Owner, Name: config.Name})
}

// StartDispatcherWithExecutors keeps one queue-discovery goroutine separate
// from a bounded executor pool. scan is never asked for more work than the pool
// can immediately hold.
func StartDispatcherWithExecutors[T any](ctx context.Context, config DispatcherExecutorConfig, scan func(context.Context, int) ([]T, error), execute func(context.Context, T) (TickResult, error)) <-chan struct{} {
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
	identity := config.identity()
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
						_, _ = observeTick(ctx, identity, "execute", func(executeCtx context.Context) (TickResult, error) {
							return execute(executeCtx, job)
						})
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
			worked := false
			if available > 0 {
				var items []T
				result, err := observeTick(ctx, identity, "scan", func(scanCtx context.Context) (TickResult, error) {
					var scanErr error
					items, scanErr = scan(scanCtx, available)
					if len(items) > available {
						items = items[:available]
					}
					return TickResult{Worked: len(items) > 0, ProcessedCount: len(items)}, scanErr
				})
				if err == nil {
					worked = result.Worked
					for _, item := range items {
						slots <- struct{}{}
						jobs <- item
					}
				}
			}
			interval = nextAdaptiveInterval(interval, config.MinInterval, config.MaxInterval, worked)
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

// StartWakeableDispatcherWithExecutors dispatches committed task locators to
// a bounded executor pool and runs a low-frequency durable recovery query.
func StartWakeableDispatcherWithExecutors[T any](ctx context.Context, config DispatcherExecutorConfig, recoveryInterval time.Duration, wakeups <-chan T, recover func(context.Context, int) ([]T, error), execute func(context.Context, T) (TickResult, error)) <-chan struct{} {
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
	identity := config.identity()
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
						_, _ = observeTick(ctx, identity, "execute", func(executeCtx context.Context) (TickResult, error) {
							return execute(executeCtx, job)
						})
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
			var items []T
			_, err := observeTick(ctx, identity, "recovery", func(recoveryCtx context.Context) (TickResult, error) {
				var recoveryErr error
				items, recoveryErr = recover(recoveryCtx, available)
				if len(items) > available {
					items = items[:available]
				}
				return TickResult{Worked: len(items) > 0, ProcessedCount: len(items)}, recoveryErr
			})
			if err != nil {
				return
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
