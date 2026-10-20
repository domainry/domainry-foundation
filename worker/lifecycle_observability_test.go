package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestObserveTickLogsStableFailureOnce(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	want := apperror.New(apperror.KindUnavailable, "worker.scan_unavailable", errors.New("password=secret"), nil)
	result, err := observeTick(t.Context(), Identity{Owner: "scheduler", Name: "dispatch"}, "scan", func(context.Context) (TickResult, error) {
		return TickResult{Attempt: 2, ProcessedCount: 0}, want
	})
	if !errors.Is(err, want) || result.Attempt != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	entries := observed.All()
	if len(entries) != 1 || entries[0].Message != "worker_scan_failed" {
		t.Fatalf("entries=%#v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["owner"] != "scheduler" || fields["worker"] != "dispatch" || fields["attempt"] != int64(2) || fields["error_code"] != "worker.scan_unavailable" || fields["failure_stage"] != "worker_scan" || fields["retryable"] != true {
		t.Fatalf("fields=%#v", fields)
	}
	if strings.Contains(fmt.Sprint(fields), "secret") {
		t.Fatalf("raw error leaked: %#v", fields)
	}
}

func TestObserveTickClassifiesRawFailureWithWorkerIdentity(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	raw := errors.New("database secret")
	_, err := observeTick(t.Context(), Identity{Owner: "data_exchange", Name: "job_processor"}, "tick", func(context.Context) (TickResult, error) {
		return TickResult{}, raw
	})
	if !errors.Is(err, raw) {
		t.Fatalf("raw cause was not preserved: %v", err)
	}
	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("entries=%#v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["error_code"] != "backend.data_exchange.job_processor_tick_failed" || fields["error_kind"] != "internal" {
		t.Fatalf("fields=%#v", fields)
	}
	if strings.Contains(fmt.Sprint(fields), "secret") {
		t.Fatalf("raw error leaked: %#v", fields)
	}
}

func TestObserveTickPanicDoesNotLogPanicValue(t *testing.T) {
	core, observed := observer.New(zapcore.ErrorLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	_, err := observeTick(t.Context(), Identity{Owner: "notification", Name: "delivery"}, "execute", func(context.Context) (TickResult, error) {
		panic("authorization=secret")
	})
	if err == nil || apperror.CodeOf(err) != "worker.panic" {
		t.Fatalf("panic error=%v", err)
	}
	entries := observed.All()
	if len(entries) != 1 || entries[0].Message != "panic_recovered" {
		t.Fatalf("entries=%#v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["panic_type"] != "string" || strings.Contains(fmt.Sprint(fields), "authorization=secret") {
		t.Fatalf("panic fields=%#v", fields)
	}
}

func TestObserveTickLogsTerminalFailureAtError(t *testing.T) {
	core, observed := observer.New(zapcore.ErrorLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	_, _ = observeTick(t.Context(), Identity{Owner: "identity", Name: "reconcile"}, "execute", func(context.Context) (TickResult, error) {
		return TickResult{}, apperror.New(apperror.KindBadRequest, "identity.reconcile_invalid", nil, nil)
	})
	entries := observed.All()
	if len(entries) != 1 || entries[0].Level != zapcore.ErrorLevel || entries[0].ContextMap()["retryable"] != false {
		t.Fatalf("terminal entries=%#v", entries)
	}
}

func TestObserveTickSuppressesProcessCancellationError(t *testing.T) {
	core, observed := observer.New(zapcore.WarnLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _ = observeTick(ctx, Identity{Owner: "runtime", Name: "shutdown"}, "tick", func(context.Context) (TickResult, error) {
		return TickResult{}, context.Canceled
	})
	if entries := observed.All(); len(entries) != 0 {
		t.Fatalf("shutdown cancellation logged as failure: %#v", entries)
	}
}
