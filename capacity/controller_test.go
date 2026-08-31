package capacity

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestControllerEnforcesHierarchyAndWorkspaceFairness(t *testing.T) {
	controller := NewController(Limits{GlobalInFlight: 4, WorkspaceInFlight: 2, UseCaseInFlight: 3, RetryInFlight: 1}, nil)
	first, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "hot", UseCase: "mutation", Essential: true})
	if !decision.Allowed {
		t.Fatal(decision)
	}
	second, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "hot", UseCase: "mutation", Essential: true})
	if !decision.Allowed {
		t.Fatal(decision)
	}
	if _, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "hot", UseCase: "mutation", Essential: true}); decision.Allowed || decision.Dimension != DimensionWorkspace {
		t.Fatalf("hot workspace not rejected: %#v", decision)
	}
	other, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "other", UseCase: "mutation", Essential: true})
	if !decision.Allowed {
		t.Fatalf("other workspace starved: %#v", decision)
	}
	first.Release()
	second.Release()
	other.Release()
}

func TestControllerSeparatesRetryBudget(t *testing.T) {
	controller := NewController(Limits{GlobalInFlight: 8, WorkspaceInFlight: 8, UseCaseInFlight: 8, RetryInFlight: 1}, nil)
	retry, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "a", UseCase: "connector", Retry: true})
	if !decision.Allowed {
		t.Fatal(decision)
	}
	if _, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "b", UseCase: "connector", Retry: true}); decision.Allowed || decision.Code != "capacity.retry_budget_exhausted" {
		t.Fatalf("retry storm not bounded: %#v", decision)
	}
	normal, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "b", UseCase: "connector"})
	if !decision.Allowed {
		t.Fatalf("retry consumed normal capacity: %#v", decision)
	}
	retry.Release()
	normal.Release()
}

func TestControllerEnforcesWorkspaceRateAndRecoversNextWindow(t *testing.T) {
	now := time.Now()
	controller := NewController(Limits{GlobalInFlight: 8, WorkspaceInFlight: 8, UseCaseInFlight: 8, GlobalRate: 10, WorkspaceRate: 2, UseCaseRate: 10, RateWindow: time.Minute}, nil)
	controller.now = func() time.Time { return now }
	for index := 0; index < 2; index++ {
		lease, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "hot", UseCase: "mutation"})
		if !decision.Allowed {
			t.Fatal(decision)
		}
		lease.Release()
	}
	if _, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "hot", UseCase: "mutation"}); decision.Allowed || decision.Code != "capacity.workspace_rate_exceeded" {
		t.Fatalf("workspace rate was not enforced: %#v", decision)
	}
	other, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "other", UseCase: "mutation"})
	if !decision.Allowed {
		t.Fatalf("hot workspace rate starved another workspace: %#v", decision)
	}
	other.Release()
	now = now.Add(time.Minute)
	lease, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "hot", UseCase: "mutation"})
	if !decision.Allowed {
		t.Fatalf("rate window did not recover: %#v", decision)
	}
	lease.Release()
}

func TestControllerUsesHysteresisAndShedsNonEssentialWork(t *testing.T) {
	events := []Event{}
	controller := NewController(Limits{GlobalInFlight: 4, WorkspaceInFlight: 4, UseCaseInFlight: 4, DegradedRatio: .5, RecoveryRatio: .25}, func(event Event) { events = append(events, event) })
	one, _ := controller.Acquire(t.Context(), Request{WorkspaceID: "a", UseCase: "core", Essential: true})
	two, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "b", UseCase: "core", Essential: true})
	if !decision.Allowed || controller.State() != Degraded {
		t.Fatalf("did not degrade: %#v", controller.Snapshot())
	}
	if _, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "c", UseCase: "report", Essential: false}); decision.Allowed || decision.Code != "capacity.nonessential_shed" {
		t.Fatalf("nonessential work not shed: %#v", decision)
	}
	if controller.State() != Degraded {
		t.Fatalf("shedding nonessential work escalated state: %#v", controller.Snapshot())
	}
	one.Release()
	if controller.State() != Normal {
		t.Fatalf("did not recover below hysteresis: %#v", controller.Snapshot())
	}
	two.Release()
	if len(events) < 2 || events[len(events)-1].Code != "capacity.recovered" {
		t.Fatalf("overload/recovery events missing: %#v", events)
	}
}

func TestControllerStaysBoundedAndRecoversAfterConcurrentPressure(t *testing.T) {
	controller := NewController(Limits{GlobalInFlight: 32, WorkspaceInFlight: 2, UseCaseInFlight: 32, MaxWorkspaceStates: 64, MaxUseCaseStates: 8}, nil)
	var wait sync.WaitGroup
	for index := 0; index < 2_000; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			lease, _ := controller.Acquire(t.Context(), Request{WorkspaceID: fmt.Sprintf("workspace-%d", index), UseCase: fmt.Sprintf("use-case-%d", index%8), Essential: true})
			if lease != nil {
				lease.Release()
			}
		}(index)
	}
	wait.Wait()
	snapshot := controller.Snapshot()
	if snapshot.GlobalInFlight != 0 || snapshot.WorkspaceStates > 64 || snapshot.UseCaseStates > 8 {
		t.Fatalf("capacity state grew or leaked after pressure: %#v", snapshot)
	}
	if snapshot.State != Normal {
		t.Fatalf("controller did not recover after pressure: %#v", snapshot)
	}
}

func TestControllerEvictsIdleWorkspaceState(t *testing.T) {
	now := time.Now()
	controller := NewController(Limits{MaxWorkspaceStates: 1, WorkspaceStateTTL: time.Minute}, nil)
	controller.now = func() time.Time { return now }
	lease, _ := controller.Acquire(t.Context(), Request{WorkspaceID: "old", UseCase: "core"})
	lease.Release()
	now = now.Add(2 * time.Minute)
	lease, decision := controller.Acquire(t.Context(), Request{WorkspaceID: "new", UseCase: "core"})
	if !decision.Allowed {
		t.Fatalf("idle state was not evicted: %#v", decision)
	}
	lease.Release()
}
