// Package testkit contains explicit deterministic reliability dependencies.
// It is never imported by production Bootstrap wiring.
package testkit

import (
	"context"
	"errors"
	"sync"
	"time"

	worker "github.com/domainry/domainry-foundation/worker"
)

type FaultEffect struct {
	Point worker.FaultPoint
	Delay time.Duration
	Err   error
}
type ScriptedFaultInjector struct {
	mu       sync.Mutex
	effects  []FaultEffect
	observed []worker.FaultPoint
}

func NewScriptedFaultInjector(effects ...FaultEffect) *ScriptedFaultInjector {
	return &ScriptedFaultInjector{effects: append([]FaultEffect(nil), effects...)}
}
func (s *ScriptedFaultInjector) Check(ctx context.Context, point worker.FaultPoint) error {
	s.mu.Lock()
	s.observed = append(s.observed, point)
	index := -1
	var effect FaultEffect
	for i, candidate := range s.effects {
		if candidate.Point == point {
			index = i
			effect = candidate
			break
		}
	}
	if index >= 0 {
		s.effects = append(s.effects[:index], s.effects[index+1:]...)
	}
	s.mu.Unlock()
	if index < 0 {
		return nil
	}
	if effect.Delay > 0 {
		timer := time.NewTimer(effect.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return effect.Err
}
func (s *ScriptedFaultInjector) Observed() []worker.FaultPoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]worker.FaultPoint(nil), s.observed...)
}

type FixedClock struct{ Value time.Time }

func (c *FixedClock) Now() time.Time                 { return c.Value }
func (c *FixedClock) Advance(duration time.Duration) { c.Value = c.Value.Add(duration) }

type SequenceIDGenerator struct {
	mu  sync.Mutex
	IDs []string
}

func (g *SequenceIDGenerator) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.IDs) == 0 {
		return ""
	}
	id := g.IDs[0]
	g.IDs = g.IDs[1:]
	return id
}

type SequenceRandomSource struct {
	mu     sync.Mutex
	Values []int64
}

func (r *SequenceRandomSource) Int63n(max int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if max <= 0 || len(r.Values) == 0 {
		return 0
	}
	value := r.Values[0]
	r.Values = r.Values[1:]
	if value < 0 {
		value = -value
	}
	return value % max
}

func CrashBeforeCommit() FaultEffect {
	return FaultEffect{Point: worker.FaultTransactionBeforeCommit, Err: errors.New("fault.crash_before_commit")}
}
func CrashAfterCommit() FaultEffect {
	return FaultEffect{Point: worker.FaultTransactionAfterCommit, Err: errors.New("fault.crash_after_commit")}
}
func LeaseExpired() FaultEffect {
	return FaultEffect{Point: worker.FaultWorkerHeartbeat, Err: errors.New("fault.lease_expired")}
}
func StaleOwner() FaultEffect {
	return FaultEffect{Point: worker.FaultWorkerBeforeComplete, Err: errors.New("fault.stale_owner")}
}
func DBDeadlock() FaultEffect {
	return FaultEffect{Point: worker.FaultTransactionBeforeCommit, Err: errors.New("fault.db_deadlock")}
}
func DBLockTimeout() FaultEffect {
	return FaultEffect{Point: worker.FaultTransactionBeforeWrite, Err: errors.New("fault.db_lock_timeout")}
}
func DBConnectionLoss() FaultEffect {
	return FaultEffect{Point: worker.FaultTransactionAfterBegin, Err: errors.New("fault.db_connection_loss")}
}
func DBSlowQuery(delay time.Duration) FaultEffect {
	return FaultEffect{Point: worker.FaultTransactionBeforeWrite, Delay: delay}
}
func ConnectorTimeout() FaultEffect {
	return FaultEffect{Point: worker.FaultProviderBeforeSend, Err: context.DeadlineExceeded}
}
func Connector429() FaultEffect {
	return FaultEffect{Point: worker.FaultProviderBeforeSend, Err: errors.New("http_429 rate limited")}
}
func Connector5xx() FaultEffect {
	return FaultEffect{Point: worker.FaultProviderBeforeSend, Err: errors.New("http_503 provider unavailable")}
}
func ConnectorPartialResponse() FaultEffect {
	return FaultEffect{Point: worker.FaultProviderAfterSend, Err: errors.New("partial response")}
}
func ConnectorUncertainSuccess() FaultEffect {
	return FaultEffect{Point: worker.FaultProviderAfterSend, Err: errors.New("outcome uncertain after provider success")}
}
