package mutation

import (
	"strings"
	"sync"
	"time"
)

type TransactionObservation struct {
	WorkspaceID   string
	Scope         string
	CorrelationID string
	Duration      time.Duration
	Attempts      int64
	Conflicts     int64
	Rollbacks     int64
	Retries       int64
	Succeeded     bool
}

type TransactionMetricsSnapshot struct {
	Transactions  int64
	Succeeded     int64
	Failed        int64
	TotalDuration time.Duration
	Attempts      int64
	Conflicts     int64
	Rollbacks     int64
	Retries       int64
}

type TransactionMetricsCollector interface {
	ObserveTransaction(TransactionObservation)
	TransactionMetricsSnapshot() TransactionMetricsSnapshot
}

type MemoryTransactionMetricsCollector struct {
	mu       sync.RWMutex
	snapshot TransactionMetricsSnapshot
}

func NewMemoryTransactionMetricsCollector() *MemoryTransactionMetricsCollector {
	return &MemoryTransactionMetricsCollector{}
}

func (collector *MemoryTransactionMetricsCollector) ObserveTransaction(observation TransactionObservation) {
	if collector == nil || observation.Attempts <= 0 {
		return
	}
	observation.WorkspaceID = strings.TrimSpace(observation.WorkspaceID)
	observation.Scope = strings.TrimSpace(observation.Scope)
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.snapshot.Transactions++
	if observation.Succeeded {
		collector.snapshot.Succeeded++
	} else {
		collector.snapshot.Failed++
	}
	collector.snapshot.TotalDuration += observation.Duration
	collector.snapshot.Attempts += observation.Attempts
	collector.snapshot.Conflicts += observation.Conflicts
	collector.snapshot.Rollbacks += observation.Rollbacks
	collector.snapshot.Retries += observation.Retries
}

func (collector *MemoryTransactionMetricsCollector) TransactionMetricsSnapshot() TransactionMetricsSnapshot {
	if collector == nil {
		return TransactionMetricsSnapshot{}
	}
	collector.mu.RLock()
	defer collector.mu.RUnlock()
	return collector.snapshot
}
