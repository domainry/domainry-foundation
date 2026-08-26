package idempotency

import (
	"sort"
	"strings"
	"sync"
)

type Outcome string

const (
	OutcomeAcquired            Outcome = "acquired"
	OutcomeReplayed            Outcome = "replayed"
	OutcomeInProgress          Outcome = "in_progress"
	OutcomeConflict            Outcome = "conflict"
	OutcomeReclaimed           Outcome = "reclaimed"
	OutcomeLeaseLost           Outcome = "lease_lost"
	OutcomeDuplicateSideEffect Outcome = "duplicate_side_effect"
)

type Metric struct {
	WorkspaceID string  `json:"workspace_id"`
	Scope       string  `json:"scope"`
	Outcome     Outcome `json:"outcome"`
	Count       int64   `json:"count"`
}

type MetricsSnapshot struct {
	Counters      []Metric          `json:"counters"`
	Totals        map[Outcome]int64 `json:"totals"`
	SeriesCount   int               `json:"series_count"`
	DroppedSeries int64             `json:"dropped_series"`
}

type MetricsCollector interface {
	Observe(workspaceID, scope string, outcome Outcome)
	Snapshot() MetricsSnapshot
}

type MemoryMetricsCollector struct {
	mu            sync.RWMutex
	series        map[string]Metric
	maxSeries     int
	droppedSeries int64
}

func NewMemoryMetricsCollector(maxSeries int) *MemoryMetricsCollector {
	if maxSeries <= 0 {
		maxSeries = 4096
	}
	return &MemoryMetricsCollector{series: map[string]Metric{}, maxSeries: maxSeries}
}

func (c *MemoryMetricsCollector) Observe(workspaceID, scope string, outcome Outcome) {
	workspaceID, scope = strings.TrimSpace(workspaceID), strings.TrimSpace(scope)
	if workspaceID == "" {
		workspaceID = "default"
	}
	if scope == "" || !validOutcome(outcome) {
		return
	}
	key := workspaceID + "\x00" + scope + "\x00" + string(outcome)
	c.mu.Lock()
	defer c.mu.Unlock()
	metric, exists := c.series[key]
	if !exists && len(c.series) >= c.maxSeries {
		c.droppedSeries++
		return
	}
	if !exists {
		metric = Metric{WorkspaceID: workspaceID, Scope: scope, Outcome: outcome}
	}
	metric.Count++
	c.series[key] = metric
}

func (c *MemoryMetricsCollector) Snapshot() MetricsSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	snapshot := MetricsSnapshot{Counters: make([]Metric, 0, len(c.series)), Totals: map[Outcome]int64{}, SeriesCount: len(c.series), DroppedSeries: c.droppedSeries}
	for _, metric := range c.series {
		snapshot.Counters = append(snapshot.Counters, metric)
		snapshot.Totals[metric.Outcome] += metric.Count
	}
	sort.Slice(snapshot.Counters, func(i, j int) bool {
		left, right := snapshot.Counters[i], snapshot.Counters[j]
		if left.WorkspaceID != right.WorkspaceID {
			return left.WorkspaceID < right.WorkspaceID
		}
		if left.Scope != right.Scope {
			return left.Scope < right.Scope
		}
		return left.Outcome < right.Outcome
	})
	return snapshot
}

func OutcomeForDecision(decision Decision, reclaimed bool) Outcome {
	if decision == DecisionAcquired && reclaimed {
		return OutcomeReclaimed
	}
	switch decision {
	case DecisionAcquired:
		return OutcomeAcquired
	case DecisionReplay:
		return OutcomeReplayed
	case DecisionInProgress:
		return OutcomeInProgress
	case DecisionFingerprintConflict:
		return OutcomeConflict
	default:
		return ""
	}
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeAcquired, OutcomeReplayed, OutcomeInProgress, OutcomeConflict, OutcomeReclaimed, OutcomeLeaseLost, OutcomeDuplicateSideEffect:
		return true
	default:
		return false
	}
}
