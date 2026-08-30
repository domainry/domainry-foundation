package worker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var workerTickDurationBuckets = [...]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

type tickMetric struct {
	Owner, Outcome string
	Count          uint64
	Sum            float64
	Buckets        []uint64
}

type OperationalMetrics struct {
	mu            sync.RWMutex
	ticks         map[string]tickMetric
	inFlight      map[string]int64
	queueDepth    map[string]int64
	queueLag      map[string]float64
	outcomes      map[string]uint64
	maxOwners     int
	droppedOwners uint64
}

var defaultOperationalMetrics = NewOperationalMetrics(128)

func NewOperationalMetrics(maxOwners int) *OperationalMetrics {
	if maxOwners <= 0 {
		maxOwners = 128
	}
	return &OperationalMetrics{ticks: map[string]tickMetric{}, inFlight: map[string]int64{}, queueDepth: map[string]int64{}, queueLag: map[string]float64{}, outcomes: map[string]uint64{}, maxOwners: maxOwners}
}

func SetQueueMetrics(owner string, depth int, lag time.Duration) {
	defaultOperationalMetrics.setQueue(owner, depth, lag)
}

func ObserveOutcome(owner, outcome string) {
	defaultOperationalMetrics.observeOutcome(owner, outcome)
}

func (m *OperationalMetrics) setQueue(owner string, depth int, lag time.Duration) {
	if m == nil {
		return
	}
	owner = boundedWorkerOwner(owner)
	if depth < 0 {
		depth = 0
	}
	if lag < 0 {
		lag = 0
	}
	m.mu.Lock()
	if _, exists := m.queueDepth[owner]; !exists && len(m.queueDepth) >= m.maxOwners {
		m.droppedOwners++
		m.mu.Unlock()
		return
	}
	m.queueDepth[owner], m.queueLag[owner] = int64(depth), lag.Seconds()
	m.mu.Unlock()
}

func (m *OperationalMetrics) observeOutcome(owner, outcome string) {
	if m == nil {
		return
	}
	owner, outcome = boundedWorkerOwner(owner), boundedWorkerOutcome(outcome)
	m.mu.Lock()
	if _, exists := m.queueDepth[owner]; !exists && len(m.queueDepth) >= m.maxOwners {
		m.droppedOwners++
		m.mu.Unlock()
		return
	}
	if _, exists := m.queueDepth[owner]; !exists {
		m.queueDepth[owner] = 0
		m.queueLag[owner] = 0
	}
	m.outcomes[owner+"\x00"+outcome]++
	m.mu.Unlock()
}

func (m *OperationalMetrics) begin(owner string) (string, bool) {
	owner = boundedWorkerOwner(owner)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.inFlight[owner]; !exists && len(m.inFlight) >= m.maxOwners {
		m.droppedOwners++
		return "", false
	}
	m.inFlight[owner]++
	return owner, true
}

func (m *OperationalMetrics) end(owner, outcome string, duration time.Duration) {
	if owner == "" {
		return
	}
	if outcome != "panic" {
		outcome = "success"
	}
	key := owner + "\x00" + outcome
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inFlight[owner] > 0 {
		m.inFlight[owner]--
	}
	metric := m.ticks[key]
	if metric.Buckets == nil {
		metric = tickMetric{Owner: owner, Outcome: outcome, Buckets: make([]uint64, len(workerTickDurationBuckets))}
	}
	metric.Count++
	metric.Sum += duration.Seconds()
	for index, bound := range workerTickDurationBuckets {
		if duration.Seconds() <= bound {
			metric.Buckets[index]++
		}
	}
	m.ticks[key] = metric
}

func OpenMetrics(_ context.Context) string { return defaultOperationalMetrics.openMetrics() }

func (m *OperationalMetrics) openMetrics() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	ticks := make([]tickMetric, 0, len(m.ticks))
	for _, metric := range m.ticks {
		metric.Buckets = append([]uint64(nil), metric.Buckets...)
		ticks = append(ticks, metric)
	}
	inFlight := make(map[string]int64, len(m.inFlight))
	for owner, count := range m.inFlight {
		inFlight[owner] = count
	}
	queueDepth := make(map[string]int64, len(m.queueDepth))
	queueLag := make(map[string]float64, len(m.queueLag))
	outcomes := make(map[string]uint64, len(m.outcomes))
	for owner, count := range m.queueDepth {
		queueDepth[owner] = count
		queueLag[owner] = m.queueLag[owner]
	}
	for key, count := range m.outcomes {
		outcomes[key] = count
	}
	dropped := m.droppedOwners
	m.mu.RUnlock()
	sort.Slice(ticks, func(i, j int) bool {
		if ticks[i].Owner != ticks[j].Owner {
			return ticks[i].Owner < ticks[j].Owner
		}
		return ticks[i].Outcome < ticks[j].Outcome
	})
	owners := make([]string, 0, len(inFlight))
	for owner := range inFlight {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	var output strings.Builder
	output.WriteString("# HELP domainry_runtime_worker_tick_in_flight Process-owned worker ticks currently running.\n# TYPE domainry_runtime_worker_tick_in_flight gauge\n")
	for _, owner := range owners {
		fmt.Fprintf(&output, "domainry_runtime_worker_tick_in_flight{owner=%q} %d\n", owner, inFlight[owner])
	}
	queueOwners := make([]string, 0, len(queueDepth))
	for owner := range queueDepth {
		queueOwners = append(queueOwners, owner)
	}
	sort.Strings(queueOwners)
	output.WriteString("# HELP domainry_runtime_worker_owner_queue_depth Runnable durable tasks observed by owner.\n# TYPE domainry_runtime_worker_owner_queue_depth gauge\n")
	for _, owner := range queueOwners {
		fmt.Fprintf(&output, "domainry_runtime_worker_owner_queue_depth{owner=%q} %d\n", owner, queueDepth[owner])
	}
	output.WriteString("# HELP domainry_runtime_worker_owner_queue_lag_seconds Age of the oldest runnable durable task observed by owner.\n# TYPE domainry_runtime_worker_owner_queue_lag_seconds gauge\n")
	for _, owner := range queueOwners {
		fmt.Fprintf(&output, "domainry_runtime_worker_owner_queue_lag_seconds{owner=%q} %.9f\n", owner, queueLag[owner])
	}
	outcomeKeys := make([]string, 0, len(outcomes))
	for key := range outcomes {
		outcomeKeys = append(outcomeKeys, key)
	}
	sort.Strings(outcomeKeys)
	output.WriteString("# HELP domainry_runtime_worker_owner_outcomes_total Durable worker owner outcomes.\n# TYPE domainry_runtime_worker_owner_outcomes_total counter\n")
	for _, key := range outcomeKeys {
		parts := strings.SplitN(key, "\x00", 2)
		fmt.Fprintf(&output, "domainry_runtime_worker_owner_outcomes_total{owner=%q,outcome=%q} %d\n", parts[0], parts[1], outcomes[key])
	}
	output.WriteString("# HELP domainry_runtime_worker_tick_duration_seconds Process-owned worker tick duration and panic outcome.\n# TYPE domainry_runtime_worker_tick_duration_seconds histogram\n")
	for _, metric := range ticks {
		labels := fmt.Sprintf("owner=%q,outcome=%q", metric.Owner, metric.Outcome)
		for index, bound := range workerTickDurationBuckets {
			fmt.Fprintf(&output, "domainry_runtime_worker_tick_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(bound, 'f', -1, 64), metric.Buckets[index])
		}
		fmt.Fprintf(&output, "domainry_runtime_worker_tick_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, metric.Count)
		fmt.Fprintf(&output, "domainry_runtime_worker_tick_duration_seconds_sum{%s} %.9f\n", labels, metric.Sum)
		fmt.Fprintf(&output, "domainry_runtime_worker_tick_duration_seconds_count{%s} %d\n", labels, metric.Count)
	}
	fmt.Fprintf(&output, "domainry_runtime_telemetry_dropped_series_total{signal=\"worker_owner\"} %d\n", dropped)
	return output.String()
}

func boundedWorkerOwner(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 {
		return "unknown"
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return "unknown"
	}
	return value
}

func boundedWorkerOutcome(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "claimed", "completed", "retry", "dead_letter", "quarantined", "cancelled", "lease_lost", "failed", "skipped":
		return value
	default:
		return "unknown"
	}
}
