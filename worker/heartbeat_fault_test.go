package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	worker "github.com/domainry/domainry-foundation/worker"
	workertestkit "github.com/domainry/domainry-foundation/worker/testkit"
)

func TestInjectedLeaseExpiryCancelsHeartbeatBeforeComplete(t *testing.T) {
	injector := workertestkit.NewScriptedFaultInjector(workertestkit.LeaseExpired())
	workCtx, stop := worker.WithHeartbeat(t.Context(), time.Millisecond, func(ctx context.Context) error { return worker.CheckFault(ctx, injector, worker.FaultWorkerHeartbeat) })
	select {
	case <-workCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("heartbeat fault did not cancel work")
	}
	if err := stop(); err == nil || !errors.Is(workCtx.Err(), context.Canceled) {
		t.Fatalf("stop err=%v context=%v", err, workCtx.Err())
	}
}
