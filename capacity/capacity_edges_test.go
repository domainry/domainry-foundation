package capacity

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestControllerEdgeDecisionsAndSafeRelease(t *testing.T) {
	var nilLease *Lease
	nilLease.Release()
	(&Lease{}).Release()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	controller := NewController(Limits{}, nil)
	if lease, decision := controller.Acquire(cancelled, Request{}); lease != nil || decision.Code != "capacity.request_cancelled" {
		t.Fatalf("cancelled acquisition: lease=%v decision=%+v", lease, decision)
	}

	controller.release(Request{WorkspaceID: "missing", UseCase: "missing", Retry: true})
	if controller.global != 0 || controller.retries != 0 {
		t.Fatalf("empty release changed counters: %+v", controller.Snapshot())
	}

	if got := boundedKey(" ", "fallback"); got != "fallback" {
		t.Fatalf("blank key=%q", got)
	}
	if got := boundedKey(strings.Repeat("x", 129), "fallback"); got != "fallback" {
		t.Fatalf("long key=%q", got)
	}
	values := map[string]counter{"zero": {}}
	decrement(values, "zero")
	if values["zero"].inFlight != 0 {
		t.Fatalf("zero counter decremented: %+v", values["zero"])
	}
}

func TestControllerRateAndStateCapacityDecisions(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		limits Limits
		first  Request
		second Request
		code   string
	}{
		{
			name:   "global in flight",
			limits: Limits{GlobalInFlight: 1, WorkspaceInFlight: 10, UseCaseInFlight: 10, GlobalRate: 10, WorkspaceRate: 10, UseCaseRate: 10},
			first:  Request{WorkspaceID: "one", UseCase: "one", Essential: true},
			second: Request{WorkspaceID: "two", UseCase: "two", Essential: true},
			code:   "capacity.process_overloaded",
		},
		{
			name:   "global rate",
			limits: Limits{GlobalInFlight: 10, WorkspaceInFlight: 10, UseCaseInFlight: 10, GlobalRate: 1, WorkspaceRate: 10, UseCaseRate: 10},
			first:  Request{WorkspaceID: "one", UseCase: "one"},
			second: Request{WorkspaceID: "two", UseCase: "two"},
			code:   "capacity.process_rate_exceeded",
		},
		{
			name:   "use case in flight",
			limits: Limits{GlobalInFlight: 10, WorkspaceInFlight: 10, UseCaseInFlight: 1, GlobalRate: 10, WorkspaceRate: 10, UseCaseRate: 10},
			first:  Request{WorkspaceID: "one", UseCase: "shared"},
			second: Request{WorkspaceID: "two", UseCase: "shared"},
			code:   "capacity.use_case_overloaded",
		},
		{
			name:   "use case rate",
			limits: Limits{GlobalInFlight: 10, WorkspaceInFlight: 10, UseCaseInFlight: 10, GlobalRate: 10, WorkspaceRate: 10, UseCaseRate: 1},
			first:  Request{WorkspaceID: "one", UseCase: "shared"},
			second: Request{WorkspaceID: "two", UseCase: "shared"},
			code:   "capacity.use_case_rate_exceeded",
		},
		{
			name:   "use case state limit",
			limits: Limits{GlobalInFlight: 10, WorkspaceInFlight: 10, UseCaseInFlight: 10, GlobalRate: 10, WorkspaceRate: 10, UseCaseRate: 10, MaxUseCaseStates: 1},
			first:  Request{WorkspaceID: "one", UseCase: "one"},
			second: Request{WorkspaceID: "one", UseCase: "two"},
			code:   "capacity.use_case_state_exhausted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := NewController(test.limits, nil)
			controller.now = func() time.Time { return now }
			lease, decision := controller.Acquire(t.Context(), test.first)
			if !decision.Allowed {
				t.Fatalf("first decision=%+v", decision)
			}
			if test.name != "global in flight" && test.name != "use case in flight" {
				lease.Release()
			}
			if _, decision = controller.Acquire(t.Context(), test.second); decision.Allowed || decision.Code != test.code {
				t.Fatalf("second decision=%+v want %q", decision, test.code)
			}
		})
	}
}

func TestControllerStateTransitionsAndLimitNormalization(t *testing.T) {
	controller := NewController(Limits{GlobalInFlight: 10, DegradedRatio: .8, RecoveryRatio: .6}, nil)
	controller.state = Overloaded
	controller.global = 7
	controller.updateStateLocked()
	if controller.state != Degraded {
		t.Fatalf("overloaded state=%s", controller.state)
	}
	controller.state = Overloaded
	controller.global = 10
	controller.updateStateLocked()
	if controller.state != Overloaded {
		t.Fatalf("full controller state=%s", controller.state)
	}
	controller.state = Normal
	controller.global = 1
	controller.updateStateLocked()
	if controller.state != Normal {
		t.Fatalf("low controller state=%s", controller.state)
	}
	controller.state = Normal
	controller.global = 10
	controller.updateStateLocked()
	if controller.state != Overloaded {
		t.Fatalf("full normal controller state=%s", controller.state)
	}
	controller.state = Degraded
	controller.global = 0
	if decision := controller.admit(Request{Essential: true}, time.Now()); !decision.Allowed {
		t.Fatalf("essential work rejected while degraded: %+v", decision)
	}

	controller.state = Overloaded
	controller.global = 8
	controller.release(Request{})
	if controller.state != Degraded {
		t.Fatalf("partial recovery state=%s", controller.state)
	}
	controller.state = Degraded
	controller.global = 1
	controller.events = nil
	controller.release(Request{})
	if controller.state != Normal || controller.recovered == 0 {
		t.Fatalf("nil-sink recovery state=%s recovered=%d", controller.state, controller.recovered)
	}

	valid := normalizeLimits(Limits{
		GlobalInFlight: 1, WorkspaceInFlight: 1, UseCaseInFlight: 1, RetryInFlight: 1,
		GlobalRate: 1, WorkspaceRate: 1, UseCaseRate: 1, RateWindow: time.Second,
		MaxWorkspaceStates: 1, MaxUseCaseStates: 1, WorkspaceStateTTL: time.Second,
		DegradedRatio: .8, RecoveryRatio: .6, RetryAfter: time.Second,
	})
	if valid.DegradedRatio != .8 || valid.RecoveryRatio != .6 {
		t.Fatalf("valid ratios changed: %+v", valid)
	}
	invalid := normalizeLimits(Limits{DegradedRatio: 1, RecoveryRatio: .9})
	if invalid.DegradedRatio != .8 || invalid.RecoveryRatio != .6 {
		t.Fatalf("invalid ratios not normalized: %+v", invalid)
	}
}
