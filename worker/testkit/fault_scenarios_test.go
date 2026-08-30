package testkit

import (
	"context"
	"errors"
	"testing"
	"time"

	worker "github.com/domainry/domainry-foundation/worker"
)

func TestReliabilityScenarioCatalogCoversRequiredFaultWindows(t *testing.T) {
	scenarios := []FaultEffect{CrashBeforeCommit(), CrashAfterCommit(), LeaseExpired(), StaleOwner(), DBDeadlock(), DBLockTimeout(), DBConnectionLoss(), DBSlowQuery(time.Microsecond), ConnectorTimeout(), Connector429(), Connector5xx(), ConnectorPartialResponse(), ConnectorUncertainSuccess()}
	points := map[worker.FaultPoint]bool{}
	for _, scenario := range scenarios {
		points[scenario.Point] = true
		injector := NewScriptedFaultInjector(scenario)
		_ = injector.Check(t.Context(), scenario.Point)
		if len(injector.Observed()) != 1 {
			t.Fatalf("scenario not observed: %#v", scenario)
		}
	}
	for _, required := range []worker.FaultPoint{worker.FaultTransactionAfterBegin, worker.FaultTransactionBeforeWrite, worker.FaultTransactionBeforeCommit, worker.FaultTransactionAfterCommit, worker.FaultWorkerHeartbeat, worker.FaultWorkerBeforeComplete, worker.FaultProviderBeforeSend, worker.FaultProviderAfterSend} {
		if !points[required] {
			t.Fatalf("missing scenario for %s", required)
		}
	}
}

func TestDeterministicClockIDAndRandomSources(t *testing.T) {
	clock := &FixedClock{Value: time.Unix(1, 0).UTC()}
	clock.Advance(time.Second)
	if clock.Now() != time.Unix(2, 0).UTC() {
		t.Fatal(clock.Now())
	}
	ids := &SequenceIDGenerator{IDs: []string{"a", "b"}}
	if ids.NewID() != "a" || ids.NewID() != "b" {
		t.Fatal("id sequence")
	}
	if ids.NewID() != "" {
		t.Fatal("exhausted id sequence must return empty")
	}
	random := &SequenceRandomSource{Values: []int64{7}}
	if random.Int63n(5) != 2 {
		t.Fatal("random sequence")
	}
	if random.Int63n(5) != 0 || random.Int63n(0) != 0 {
		t.Fatal("empty or non-positive random source must return zero")
	}
	negative := &SequenceRandomSource{Values: []int64{-7}}
	if negative.Int63n(5) != 2 {
		t.Fatal("negative random value must be normalized")
	}
}

func TestScriptedFaultInjectorHonorsCancelledContextDuringDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	injector := NewScriptedFaultInjector(DBSlowQuery(time.Second))

	if err := injector.Check(ctx, worker.FaultTransactionBeforeWrite); !errors.Is(err, context.Canceled) {
		t.Fatalf("delayed fault error = %v", err)
	}
	unmatched := NewScriptedFaultInjector(CrashBeforeCommit())
	if err := unmatched.Check(t.Context(), worker.FaultProviderBeforeSend); err != nil {
		t.Fatalf("unmatched fault error = %v", err)
	}
}
