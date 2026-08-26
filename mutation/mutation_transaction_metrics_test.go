package mutation

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryTransactionMetricsCollectorAggregatesConcurrently(t *testing.T) {
	collector := NewMemoryTransactionMetricsCollector()
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			collector.ObserveTransaction(TransactionObservation{Duration: time.Millisecond, Attempts: 2, Conflicts: 1, Rollbacks: 1, Retries: 1, Succeeded: true})
		}()
	}
	wait.Wait()
	snapshot := collector.TransactionMetricsSnapshot()
	if snapshot.Transactions != 100 || snapshot.Succeeded != 100 || snapshot.Failed != 0 || snapshot.TotalDuration != 100*time.Millisecond || snapshot.Attempts != 200 || snapshot.Conflicts != 100 || snapshot.Rollbacks != 100 || snapshot.Retries != 100 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
