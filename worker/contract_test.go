package worker

import (
	"errors"
	"testing"
	"time"
)

func TestWorkerLeaseRequiresIdentityTokenAndExpiry(t *testing.T) {
	if _, err := NewWorkerID("  "); !errors.Is(err, ErrInvalidWorkerID) {
		t.Fatalf("NewWorkerID error = %v, want ErrInvalidWorkerID", err)
	}
	owner, err := NewWorkerID(" runtime-a ")
	if err != nil {
		t.Fatal(err)
	}
	if owner.String() != "runtime-a" {
		t.Fatalf("worker id = %q", owner)
	}
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	lease := Lease{Owner: owner, Token: 7, ExpiresAt: now.Add(time.Minute)}
	if !lease.Valid() || !lease.LiveAt(now) || !lease.Matches(owner, 7) {
		t.Fatalf("valid lease rejected: %+v", lease)
	}
	if lease.LiveAt(lease.ExpiresAt) || lease.Matches(owner, 6) {
		t.Fatalf("expired or stale fencing token accepted: %+v", lease)
	}
}

func TestWorkerHeartbeatAndShutdownStatesRemainTechnical(t *testing.T) {
	if !(HeartbeatResult{State: HeartbeatLeaseLost}).Lost() {
		t.Fatal("lost heartbeat was not classified")
	}
	if !ShutdownRunning.AcceptsClaims() {
		t.Fatal("running worker rejected claims")
	}
	for _, state := range []ShutdownState{ShutdownDraining, ShutdownStopped, ""} {
		if state.AcceptsClaims() {
			t.Fatalf("shutdown state %q accepted claims", state)
		}
	}
}

func TestWorkerDependenciesNormalizeForStandaloneUse(t *testing.T) {
	dependencies := NormalizeDependencies(Dependencies{})
	if !dependencies.Valid() {
		t.Fatalf("normalized dependencies are invalid: %+v", dependencies)
	}
	if now := dependencies.Clock.Now(); now.Location() != time.UTC {
		t.Fatalf("system clock location = %v, want UTC", now.Location())
	}
	if jitter := dependencies.Jitter.Duration(time.Millisecond); jitter < 0 || jitter >= time.Millisecond {
		t.Fatalf("jitter = %s, want [0, 1ms)", jitter)
	}
}
