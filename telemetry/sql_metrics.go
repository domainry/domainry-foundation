package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
)

var sqlDurationBuckets = [...]float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type sqlMetricSeries struct {
	Role, Operation, Outcome string
	Count                    uint64
	Sum                      float64
	Buckets                  []uint64
}

type SQLMetrics struct {
	mu           sync.RWMutex
	queries      map[string]sqlMetricSeries
	transactions map[string]sqlMetricSeries
}

func NewSQLMetrics() *SQLMetrics {
	return &SQLMetrics{queries: map[string]sqlMetricSeries{}, transactions: map[string]sqlMetricSeries{}}
}

func (m *SQLMetrics) ObserveQuery(role, operation string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.observe(m.queries, boundedSQLRole(role), boundedSQLOperation(operation), sqlOutcome(err), duration)
}

func (m *SQLMetrics) ObserveTransaction(role, outcome string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	if err != nil {
		outcome = sqlOutcome(err)
	} else {
		switch strings.ToLower(strings.TrimSpace(outcome)) {
		case "commit", "rollback":
		default:
			outcome = "error"
		}
	}
	m.observe(m.transactions, boundedSQLRole(role), "transaction", outcome, duration)
}

func (m *SQLMetrics) observe(target map[string]sqlMetricSeries, role, operation, outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	key := role + "\x00" + operation + "\x00" + outcome
	m.mu.Lock()
	defer m.mu.Unlock()
	series := target[key]
	if series.Buckets == nil {
		series = sqlMetricSeries{Role: role, Operation: operation, Outcome: outcome, Buckets: make([]uint64, len(sqlDurationBuckets))}
	}
	series.Count++
	series.Sum += duration.Seconds()
	for index, bound := range sqlDurationBuckets {
		if duration.Seconds() <= bound {
			series.Buckets[index]++
		}
	}
	target[key] = series
}

func (m *SQLMetrics) OpenMetrics(_ context.Context) string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	queries := cloneSQLSeries(m.queries)
	transactions := cloneSQLSeries(m.transactions)
	m.mu.RUnlock()
	var output strings.Builder
	writeSQLHistogram(&output, "domainry_db_query_duration_seconds", "Database operation duration without SQL text or relation labels.", queries)
	writeSQLHistogram(&output, "domainry_db_transaction_duration_seconds", "Database transaction duration by bounded terminal outcome.", transactions)
	return output.String()
}

func cloneSQLSeries(source map[string]sqlMetricSeries) []sqlMetricSeries {
	result := make([]sqlMetricSeries, 0, len(source))
	for _, series := range source {
		series.Buckets = append([]uint64(nil), series.Buckets...)
		result = append(result, series)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role != result[j].Role {
			return result[i].Role < result[j].Role
		}
		if result[i].Operation != result[j].Operation {
			return result[i].Operation < result[j].Operation
		}
		return result[i].Outcome < result[j].Outcome
	})
	return result
}

func writeSQLHistogram(output *strings.Builder, name, help string, values []sqlMetricSeries) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	for _, series := range values {
		labels := fmt.Sprintf("role=%q,operation=%q,outcome=%q", series.Role, series.Operation, series.Outcome)
		for index, bound := range sqlDurationBuckets {
			fmt.Fprintf(output, "%s_bucket{%s,le=%q} %d\n", name, labels, strconv.FormatFloat(bound, 'f', -1, 64), series.Buckets[index])
		}
		fmt.Fprintf(output, "%s_bucket{%s,le=\"+Inf\"} %d\n", name, labels, series.Count)
		fmt.Fprintf(output, "%s_sum{%s} %.9f\n", name, labels, series.Sum)
		fmt.Fprintf(output, "%s_count{%s} %d\n", name, labels, series.Count)
	}
}

func boundedSQLRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "runtime", "migration":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return "runtime"
	}
}

func boundedSQLOperation(operation string) string {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "select", "insert", "update", "delete", "ddl", "pragma", "transaction":
		return strings.ToLower(strings.TrimSpace(operation))
	default:
		return "other"
	}
}

func sqlOperation(query string) string {
	fields := strings.Fields(strings.TrimSpace(query))
	if len(fields) == 0 {
		return "other"
	}
	switch strings.ToUpper(fields[0]) {
	case "SELECT", "WITH", "SHOW", "EXPLAIN":
		return "select"
	case "INSERT":
		return "insert"
	case "UPDATE", "UPSERT":
		return "update"
	case "DELETE":
		return "delete"
	case "CREATE", "ALTER", "DROP", "TRUNCATE":
		return "ddl"
	case "PRAGMA":
		return "pragma"
	default:
		return "other"
	}
}

func sqlOutcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout"
	}
	for _, kind := range []mutation.TransactionTransientKind{mutation.TransactionTransientDeadlock, mutation.TransactionTransientSerializationFailure, mutation.TransactionTransientLockTimeout} {
		if mutation.IsTransactionTransient(err, kind) {
			return string(kind)
		}
	}
	if mutation.IsMutationConflict(err, "") {
		return "conflict"
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "deadlock") || strings.Contains(text, "sqlstate 40p01") || strings.Contains(text, "error 1213") {
		return "deadlock"
	}
	if strings.Contains(text, "serialization") || strings.Contains(text, "sqlstate 40001") {
		return "serialization_failure"
	}
	if strings.Contains(text, "database is locked") || strings.Contains(text, "lock timeout") || strings.Contains(text, "sqlite_busy") {
		return "lock_timeout"
	}
	if strings.Contains(text, "constraint") || strings.Contains(text, "duplicate") || strings.Contains(text, "sqlstate 23505") {
		return "conflict"
	}
	return "error"
}
