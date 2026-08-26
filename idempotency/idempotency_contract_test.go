package idempotency

import (
	"testing"
	"time"
)

func TestScopeRequiresExplicitWorkspaceAndStableUseCase(t *testing.T) {
	scope := Scope{WorkspaceID: " workspace-a ", UseCase: " action.execute ", ResourceType: " record ", TargetID: " r-1 ", Key: " key-1 "}.Normalized()
	if !scope.IsComplete() || scope.WorkspaceID != "workspace-a" || scope.UseCase != "action.execute" || scope.Key != "key-1" {
		t.Fatalf("unexpected normalized scope: %#v", scope)
	}
	if (Scope{UseCase: "action.execute", ResourceType: "record", Key: "key-1"}).IsComplete() {
		t.Fatal("scope without workspace must not be complete")
	}
}

func TestClassifyReceiptLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	live := Lease{Owner: "runtime-a", Token: 4, ExpiresAt: now.Add(time.Minute)}
	cases := []struct {
		name        string
		state       ReceiptState
		fingerprint string
		want        Decision
	}{
		{name: "success replay", state: ReceiptState{Status: StatusSucceeded, Fingerprint: "same"}, fingerprint: "same", want: DecisionReplay},
		{name: "terminal replay", state: ReceiptState{Status: StatusFailedTerminal, Fingerprint: "same"}, fingerprint: "same", want: DecisionReplay},
		{name: "live processing", state: ReceiptState{Status: StatusProcessing, Fingerprint: "same", Lease: live}, fingerprint: "same", want: DecisionInProgress},
		{name: "expired processing reclaim", state: ReceiptState{Status: StatusProcessing, Fingerprint: "same", Lease: Lease{Owner: "runtime-a", Token: 4, ExpiresAt: now.Add(-time.Second)}}, fingerprint: "same", want: DecisionAcquired},
		{name: "retryable reclaim", state: ReceiptState{Status: StatusFailedRetryable, Fingerprint: "same"}, fingerprint: "same", want: DecisionAcquired},
		{name: "fingerprint conflict first", state: ReceiptState{Status: StatusSucceeded, Fingerprint: "old"}, fingerprint: "new", want: DecisionFingerprintConflict},
		{name: "unknown status", state: ReceiptState{Status: Status("unknown"), Fingerprint: "same"}, fingerprint: "same", want: DecisionInProgress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.state, tc.fingerprint, now); got != tc.want {
				t.Fatalf("Classify()=%q want %q", got, tc.want)
			}
		})
	}
	if !live.Matches(" runtime-a ", 4) || live.Matches("runtime-b", 4) || live.Matches("runtime-a", 3) {
		t.Fatal("lease fencing match is not owner and token exact")
	}
}
