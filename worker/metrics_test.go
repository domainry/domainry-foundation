package worker

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkerOperationalMetricsAreBoundedAndConcurrent(t *testing.T) {
	metrics := NewOperationalMetrics(2)
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			owner, observed := metrics.begin("workflow_worker")
			if observed {
				metrics.end(owner, "success", time.Millisecond)
			}
		}()
	}
	group.Wait()
	owner, observed := metrics.begin("unsafe/workspace/42")
	if observed {
		metrics.end(owner, "panic", 2*time.Millisecond)
	}
	if _, observed := metrics.begin("third-owner"); observed {
		t.Fatal("owner cardinality budget was not enforced")
	}
	metrics.setQueue("workflow_worker", 7, 3*time.Second)
	metrics.observeOutcome("workflow_worker", "lease_lost")
	output := metrics.openMetrics()
	for _, expected := range []string{
		`domainry_runtime_worker_tick_in_flight{owner="workflow_worker"} 0`,
		`domainry_runtime_worker_tick_duration_seconds_count{owner="workflow_worker",outcome="success"} 100`,
		`owner="unknown",outcome="panic"`,
		`domainry_runtime_worker_owner_queue_depth{owner="workflow_worker"} 7`,
		`domainry_runtime_worker_owner_queue_lag_seconds{owner="workflow_worker"} 3.000000000`,
		`domainry_runtime_worker_owner_outcomes_total{owner="workflow_worker",outcome="lease_lost"} 1`,
		`domainry_runtime_telemetry_dropped_series_total{signal="worker_owner"} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("worker metrics missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "workspace/42") {
		t.Fatalf("unbounded worker owner leaked: %s", output)
	}
}
