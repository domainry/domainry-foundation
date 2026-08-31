package health

import (
	"context"
	"sort"
	"sync"
	"time"
)

type Criticality string

const (
	Critical Criticality = "critical"
	Optional Criticality = "optional"
)

type Check struct {
	Name        string
	Criticality Criticality
	Timeout     time.Duration
	Run         func(context.Context) error
}

type CheckSnapshot struct {
	Name          string      `json:"name"`
	Status        string      `json:"status"`
	Criticality   Criticality `json:"criticality"`
	TimeoutMS     int64       `json:"timeout_ms"`
	DurationMS    int64       `json:"duration_ms"`
	LastSuccessAt string      `json:"last_success_at,omitempty"`
	LastErrorAt   string      `json:"last_error_at,omitempty"`
	LastErrorCode string      `json:"last_error_code,omitempty"`
}

type Snapshot struct {
	Status    string          `json:"status"`
	CheckedAt string          `json:"checked_at"`
	Checks    []CheckSnapshot `json:"checks"`
}

type Registry struct {
	mu     sync.Mutex
	states map[string]CheckSnapshot
}

func NewRegistry() *Registry { return &Registry{states: map[string]CheckSnapshot{}} }

func (r *Registry) Evaluate(ctx context.Context, checks []Check) Snapshot {
	checkedAt := time.Now().UTC()
	results := make([]CheckSnapshot, 0, len(checks))
	status := "ok"
	for _, check := range checks {
		result := r.evaluate(ctx, check, checkedAt)
		results = append(results, result)
		if result.Status != "ok" {
			if check.Criticality == Critical {
				status = "unavailable"
			} else if status == "ok" {
				status = "degraded"
			}
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return Snapshot{Status: status, CheckedAt: checkedAt.Format(time.RFC3339Nano), Checks: results}
}

func (r *Registry) evaluate(parent context.Context, check Check, now time.Time) CheckSnapshot {
	timeout := check.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	result := CheckSnapshot{Name: check.Name, Criticality: check.Criticality, TimeoutMS: timeout.Milliseconds()}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		if check.Run == nil {
			done <- nil
			return
		}
		done <- check.Run(ctx)
	}()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	result.DurationMS = time.Since(start).Milliseconds()
	r.mu.Lock()
	defer r.mu.Unlock()
	previous := r.states[check.Name]
	result.LastSuccessAt, result.LastErrorAt, result.LastErrorCode = previous.LastSuccessAt, previous.LastErrorAt, previous.LastErrorCode
	if err == nil {
		result.Status = "ok"
		result.LastSuccessAt = now.Format(time.RFC3339Nano)
	} else {
		result.Status = "error"
		result.LastErrorAt = now.Format(time.RFC3339Nano)
		if ctx.Err() != nil {
			result.LastErrorCode = "timeout"
		} else {
			result.LastErrorCode = "check_failed"
		}
	}
	r.states[check.Name] = result
	return result
}
