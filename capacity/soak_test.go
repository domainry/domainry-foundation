package capacity_test

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/capacity"
	"github.com/domainry/domainry-foundation/ratelimit"
)

func TestBoundedFoundationStateSoakDoesNotGrow(t *testing.T) {
	beforeGoroutines := runtime.NumGoroutine()
	controller := capacity.NewController(capacity.Limits{GlobalInFlight: 64, WorkspaceInFlight: 4, UseCaseInFlight: 16, RetryInFlight: 8, GlobalRate: 1_000_000, WorkspaceRate: 1_000_000, UseCaseRate: 1_000_000, RateWindow: time.Minute, MaxWorkspaceStates: 128, MaxUseCaseStates: 64, WorkspaceStateTTL: time.Hour}, nil)
	limiter := ratelimit.NewMemoryLimiter(128)
	for index := 0; index < 20_000; index++ {
		workspace := fmt.Sprintf("workspace-%d", index)
		useCase := fmt.Sprintf("use-case-%d", index%64)
		lease, _ := controller.Acquire(t.Context(), capacity.Request{WorkspaceID: workspace, UseCase: useCase, Retry: index%5 == 0})
		if lease != nil {
			lease.Release()
		}
		_, _ = limiter.Allow(t.Context(), workspace, 10, time.Minute)
	}
	runtime.GC()
	snapshot, limiterStats := controller.Snapshot(), limiter.Stats()
	if snapshot.GlobalInFlight != 0 || snapshot.WorkspaceStates > 128 || snapshot.UseCaseStates > 64 {
		t.Fatalf("capacity state grew after soak: %+v", snapshot)
	}
	if limiterStats.Entries > 128 || limiterStats.Evictions == 0 {
		t.Fatalf("bounded rate-limit store grew after soak: %+v", limiterStats)
	}
	if after := runtime.NumGoroutine(); after > beforeGoroutines+4 {
		t.Fatalf("goroutines grew after soak: before=%d after=%d", beforeGoroutines, after)
	}
}
