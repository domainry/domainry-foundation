package idempotency

import (
	"sync"
	"testing"
)

func TestMemoryMetricsCollectorCountsAllOutcomesConcurrentlyAndBoundsSeries(t *testing.T) {
	collector := NewMemoryMetricsCollector(7)
	outcomes := []Outcome{OutcomeAcquired, OutcomeReplayed, OutcomeInProgress, OutcomeConflict, OutcomeReclaimed, OutcomeLeaseLost, OutcomeDuplicateSideEffect}
	var wait sync.WaitGroup
	for _, outcome := range outcomes {
		for index := 0; index < 100; index++ {
			wait.Add(1)
			go func(outcome Outcome) {
				defer wait.Done()
				collector.Observe("workspace-a", "action.execute", outcome)
			}(outcome)
		}
	}
	wait.Wait()
	snapshot := collector.Snapshot()
	if snapshot.SeriesCount != 7 || len(snapshot.Counters) != 7 || snapshot.DroppedSeries != 0 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	for _, outcome := range outcomes {
		if snapshot.Totals[outcome] != 100 {
			t.Fatalf("outcome %s=%d", outcome, snapshot.Totals[outcome])
		}
	}
	collector.Observe("workspace-b", "another.scope", OutcomeAcquired)
	if got := collector.Snapshot().DroppedSeries; got != 1 {
		t.Fatalf("dropped series=%d", got)
	}
}

func TestOutcomeForDecisionDistinguishesReclaim(t *testing.T) {
	if OutcomeForDecision(DecisionAcquired, false) != OutcomeAcquired || OutcomeForDecision(DecisionAcquired, true) != OutcomeReclaimed || OutcomeForDecision(DecisionReplay, false) != OutcomeReplayed || OutcomeForDecision(Decision("unknown"), false) != "" {
		t.Fatal("decision outcome mapping changed")
	}
}

func TestMemoryMetricsCollectorDefaultsAndRejectsInvalidSeries(t *testing.T) {
	collector := NewMemoryMetricsCollector(0)
	collector.Observe(" ", "scope", OutcomeAcquired)
	collector.Observe("workspace", " ", OutcomeAcquired)
	collector.Observe("workspace", "scope", Outcome("invalid"))
	snapshot := collector.Snapshot()
	if snapshot.SeriesCount != 1 || snapshot.Counters[0].WorkspaceID != "default" || snapshot.Counters[0].Count != 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
