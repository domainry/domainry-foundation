package worker

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
)

type workerClockStub struct{ now time.Time }

func (c workerClockStub) Now() time.Time { return c.now }

type workerIDStub struct{}

func (workerIDStub) NewID() string { return "id" }

type workerRandomStub struct{}

func (workerRandomStub) Int63n(int64) int64 { return 0 }

type workerFaultStub struct{ err error }

func (f workerFaultStub) Check(context.Context, FaultPoint) error { return f.err }

type workerErrorReader struct{ err error }

func (r workerErrorReader) Read([]byte) (int, error) { return 0, r.err }

type workerContextStub struct {
	done <-chan struct{}
	err  func() error
}

func (c workerContextStub) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c workerContextStub) Done() <-chan struct{}       { return c.done }
func (c workerContextStub) Err() error {
	if c.err == nil {
		return nil
	}
	return c.err()
}
func (workerContextStub) Value(any) any { return nil }

func TestWorkerContractAndDependencyRemainingEdges(t *testing.T) {
	if (RandomJitterSource{}).Duration(0) != 0 {
		t.Fatal("zero jitter max produced a duration")
	}
	previousReader := cryptorand.Reader
	cryptorand.Reader = workerErrorReader{err: io.ErrUnexpectedEOF}
	if (RandomJitterSource{}).Duration(time.Second) != 0 {
		t.Fatal("failed random jitter did not fall back to zero")
	}
	cryptorand.Reader = previousReader

	workerID, err := NewWorkerID("custom")
	if err != nil {
		t.Fatal(err)
	}
	dependencies := Dependencies{
		Clock: workerClockStub{}, WorkerID: workerID, Jitter: fixedJitter(0), Control: NewController(),
		IDs: workerIDStub{}, Random: workerRandomStub{}, Faults: workerFaultStub{},
	}
	if !NewDependencies(workerID).Valid() || !dependencies.Valid() {
		t.Fatal("complete dependencies rejected")
	}
	normalized := NormalizeDependencies(dependencies)
	if normalized.Clock != dependencies.Clock || normalized.Control != dependencies.Control || normalized.IDs != dependencies.IDs {
		t.Fatalf("complete dependencies changed: %+v", normalized)
	}
	invalid := []Dependencies{
		{WorkerID: workerID, Jitter: dependencies.Jitter, Control: dependencies.Control, IDs: dependencies.IDs, Random: dependencies.Random, Faults: dependencies.Faults},
		{Clock: dependencies.Clock, Jitter: dependencies.Jitter, Control: dependencies.Control, IDs: dependencies.IDs, Random: dependencies.Random, Faults: dependencies.Faults},
		{Clock: dependencies.Clock, WorkerID: workerID, Control: dependencies.Control, IDs: dependencies.IDs, Random: dependencies.Random, Faults: dependencies.Faults},
		{Clock: dependencies.Clock, WorkerID: workerID, Jitter: dependencies.Jitter, IDs: dependencies.IDs, Random: dependencies.Random, Faults: dependencies.Faults},
		{Clock: dependencies.Clock, WorkerID: workerID, Jitter: dependencies.Jitter, Control: dependencies.Control, Random: dependencies.Random, Faults: dependencies.Faults},
		{Clock: dependencies.Clock, WorkerID: workerID, Jitter: dependencies.Jitter, Control: dependencies.Control, IDs: dependencies.IDs, Faults: dependencies.Faults},
		{Clock: dependencies.Clock, WorkerID: workerID, Jitter: dependencies.Jitter, Control: dependencies.Control, IDs: dependencies.IDs, Random: dependencies.Random},
	}
	for index, value := range invalid {
		if value.Valid() {
			t.Fatalf("invalid dependencies %d accepted", index)
		}
	}
}

func TestControllerRemainingStateAndNilEdges(t *testing.T) {
	var nilController *Controller
	if snapshot := nilController.Snapshot(); snapshot.State != ShutdownStopped || snapshot.InFlight != 0 {
		t.Fatalf("nil snapshot=%+v", snapshot)
	}
	if nilController.Drain() || !nilController.TryBegin() || !nilController.WaitIdle(0) || nilController.transition(ShutdownRunning, ShutdownPaused) {
		t.Fatal("nil controller contract mismatch")
	}
	nilController.Stop()
	nilController.End()
	called := false
	if !nilController.RunIfAccepting(func() { called = true }) || !called || nilController.RunIfAccepting(nil) {
		t.Fatal("nil controller run contract mismatch")
	}
	if !strings.Contains(nilController.OpenMetrics(), `state="stopped"`) {
		t.Fatal("nil controller metrics missing stopped state")
	}

	controller := NewController()
	controller.End()
	if !controller.TryBegin() || !controller.TryBegin() || controller.WaitIdle(0) {
		t.Fatal("busy controller reported idle")
	}
	controller.End()
	if controller.WaitIdle(0) {
		t.Fatal("controller reported idle with one remaining task")
	}
	controller.End()
	if !controller.WaitIdle(0) {
		t.Fatal("idle controller did not report idle")
	}
	if !controller.Pause() || controller.RunIfAccepting(func() { t.Fatal("paused controller ran") }) || controller.Pause() {
		t.Fatal("paused transition contract mismatch")
	}
	if !controller.Resume() || !controller.Drain() || controller.Drain() {
		t.Fatal("draining transition contract mismatch")
	}
	controller.Stop()
	if controller.Drain() || controller.Resume() {
		t.Fatal("stopped controller transitioned")
	}
}

func TestFaultAndHeartbeatRemainingEdges(t *testing.T) {
	if id := (CryptoIdentifierGenerator{}).NewID(); len(id) != 32 {
		t.Fatalf("crypto id=%q", id)
	}
	if (CryptoRandomSource{}).Int63n(0) != 0 {
		t.Fatal("zero random max produced a value")
	}
	if value := (CryptoRandomSource{}).Int63n(10); value < 0 || value >= 10 {
		t.Fatalf("crypto random=%d", value)
	}
	previousReader := cryptorand.Reader
	cryptorand.Reader = workerErrorReader{err: io.ErrUnexpectedEOF}
	if (CryptoRandomSource{}).Int63n(10) != 0 {
		t.Fatal("failed crypto random did not fall back to zero")
	}
	cryptorand.Reader = previousReader

	want := errors.New("fault")
	if err := CheckFault(t.Context(), nil, FaultWorkerClaim); err != nil {
		t.Fatalf("nil fault injector=%v", err)
	}
	if err := CheckFault(t.Context(), workerFaultStub{err: want}, FaultWorkerClaim); !errors.Is(err, want) {
		t.Fatalf("injected fault=%v", err)
	}
	if err := (NoopFaultInjector{}).Check(t.Context(), FaultWorkerClaim); err != nil {
		t.Fatal(err)
	}

	_, stop := WithHeartbeat(t.Context(), 0, func(context.Context) error { return nil })
	if err := stop(); err != nil {
		t.Fatalf("default interval stop=%v", err)
	}
	calls := make(chan struct{}, 1)
	_, stop = WithHeartbeat(t.Context(), time.Millisecond, func(context.Context) error {
		select {
		case calls <- struct{}{}:
		default:
		}
		return nil
	})
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not run")
	}
	if err := stop(); err != nil || stop() != nil {
		t.Fatalf("idempotent heartbeat stop=%v", err)
	}
}

func TestLifecycleRemainingGuardAndMetricEdges(t *testing.T) {
	select {
	case <-StartNamedLoop(nil, "worker", time.Second, func() {}):
	case <-time.After(time.Second):
		t.Fatal("nil context loop did not stop")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := StartNamedLoop(ctx, " ", 0, cancel)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("default name/interval loop did not stop")
	}
	select {
	case <-Stopped():
	default:
		t.Fatal("stopped channel remained open")
	}
	closed := make(chan struct{})
	close(closed)
	select {
	case <-StartNamedLoop(workerContextStub{done: closed}, "closed", time.Millisecond, func() { t.Fatal("closed context ticked") }):
	case <-time.After(time.Second):
		t.Fatal("closed custom context loop did not stop")
	}
	open := make(chan struct{})
	cancelled := false
	done = StartNamedLoop(workerContextStub{done: open, err: func() error {
		if cancelled {
			return context.Canceled
		}
		return nil
	}}, "ticker-cancel", time.Millisecond, func() { cancelled = true })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ticker cancellation guard did not stop")
	}

	previousMetrics := defaultOperationalMetrics
	metrics := NewOperationalMetrics(1)
	if _, observed := metrics.begin("occupied"); !observed {
		t.Fatal("failed to occupy metric owner")
	}
	defaultOperationalMetrics = metrics
	runTick(t.Context(), "dropped", func() {})
	defaultOperationalMetrics = previousMetrics
}

func TestOperationalMetricsRemainingEdges(t *testing.T) {
	if NewOperationalMetrics(0).maxOwners != 128 {
		t.Fatal("default owner limit not applied")
	}
	var nilMetrics *OperationalMetrics
	nilMetrics.setQueue("owner", 1, time.Second)
	nilMetrics.observeOutcome("owner", "completed")
	if nilMetrics.openMetrics() != "" {
		t.Fatal("nil metrics emitted output")
	}

	queue := NewOperationalMetrics(1)
	queue.setQueue("one", -1, -time.Second)
	queue.setQueue("one", 2, time.Second)
	queue.setQueue("two", 1, time.Second)
	if queue.queueDepth["one"] != 2 || queue.queueLag["one"] != 1 || queue.droppedOwners != 1 {
		t.Fatalf("queue metrics=%+v lag=%+v dropped=%d", queue.queueDepth, queue.queueLag, queue.droppedOwners)
	}
	outcomes := NewOperationalMetrics(1)
	outcomes.observeOutcome("one", "completed")
	outcomes.observeOutcome("one", "failed")
	outcomes.observeOutcome("two", "completed")
	if outcomes.queueDepth["one"] != 0 || outcomes.droppedOwners != 1 {
		t.Fatalf("outcome metrics=%+v dropped=%d", outcomes.outcomes, outcomes.droppedOwners)
	}

	metrics := NewOperationalMetrics(2)
	metrics.end("", "success", time.Millisecond)
	metrics.end("owner", "other", time.Millisecond)
	owner, observed := metrics.begin("owner")
	if !observed {
		t.Fatal("owner not observed")
	}
	metrics.end(owner, "panic", 100*time.Second)
	metrics.end(owner, "success", time.Millisecond)
	output := metrics.openMetrics()
	for _, expected := range []string{`owner="owner",outcome="panic"`, `owner="owner",outcome="success"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics missing %q", expected)
		}
	}

	for _, value := range []string{"", strings.Repeat("a", 65), "bad/value", "{", ":"} {
		if boundedWorkerOwner(value) != "unknown" {
			t.Fatalf("unsafe owner %q accepted", value)
		}
	}
	if boundedWorkerOwner("5") != "5" {
		t.Fatal("numeric owner rejected")
	}
	if boundedWorkerOutcome("unexpected") != "unknown" || boundedWorkerOutcome(" COMPLETED ") != "completed" {
		t.Fatal("outcome bounding mismatch")
	}
	SetQueueMetrics("global-edge", 1, time.Second)
	ObserveOutcome("global-edge", "completed")
	if output := OpenMetrics(t.Context()); !strings.Contains(output, "global-edge") {
		t.Fatal("global metrics helper did not emit owner")
	}
}

func TestRetryRemainingClassificationAndDelayEdges(t *testing.T) {
	if !FailureTransient.Retryable() || !FailureRateLimited.Retryable() || !FailureDependencyUnavailable.Retryable() || FailureTerminal.Retryable() || FailureCancelled.Retryable() {
		t.Fatal("retryable classification mismatch")
	}
	for err, want := range map[error]FailureClass{
		context.DeadlineExceeded:                                    FailureCancelled,
		apperror.New(apperror.KindInternal, "internal", nil, nil):   FailureTransient,
		apperror.New(apperror.KindBadRequest, "terminal", nil, nil): FailureTerminal,
	} {
		if got := ClassifyApplicationError(err); got != want {
			t.Fatalf("classification %v=%q want=%q", err, got, want)
		}
	}

	now := time.Now()
	for _, policy := range []RetryPolicy{{}, {MaxAttempts: 2}, {MaxAttempts: 2, Deadline: now.Add(-time.Second)}} {
		if policy.Allows(0, now) || policy.Allows(2, now) || policy.Allows(1, now) && !policy.Deadline.IsZero() {
			t.Fatalf("invalid retry allowed: %+v", policy)
		}
	}
	if !(RetryPolicy{MaxAttempts: 2}).Allows(1, now) {
		t.Fatal("valid retry rejected")
	}
	if got := (RetryPolicy{BaseDelay: -time.Second}).Delay(1, nil); got != 0 {
		t.Fatalf("negative delay=%v", got)
	}
	if got := (RetryPolicy{BaseDelay: time.Second, MaxDelay: 10 * time.Second, Backoff: BackoffFixed}).Delay(3, nil); got != time.Second {
		t.Fatalf("fixed delay=%v", got)
	}
	if got := (RetryPolicy{BaseDelay: time.Second, MaxDelay: 10 * time.Second, Backoff: BackoffExponential}).Delay(1, nil); got != time.Second {
		t.Fatalf("first exponential delay=%v", got)
	}
	if got := (RetryPolicy{BaseDelay: 2 * time.Second, MaxDelay: time.Second, Backoff: BackoffExponential}).Delay(2, nil); got != time.Second {
		t.Fatalf("already capped exponential delay=%v", got)
	}
	if got := (RetryPolicy{BaseDelay: time.Duration(1 << 62), MaxDelay: time.Duration(1<<63 - 1), Backoff: BackoffExponential}).Delay(3, nil); got != time.Duration(1<<63-1) {
		t.Fatalf("overflow delay=%v", got)
	}
	if got := (RetryPolicy{BaseDelay: 5 * time.Second, MaxDelay: time.Second}).Delay(1, nil); got != time.Second {
		t.Fatalf("clamped delay=%v", got)
	}
	if got := (RetryPolicy{BaseDelay: time.Second, JitterMax: time.Second}).Delay(1, nil); got != time.Second {
		t.Fatalf("nil jitter delay=%v", got)
	}
	if got := (RetryPolicy{BaseDelay: time.Second, JitterMax: 0}).Delay(1, fixedJitter(time.Second)); got != time.Second {
		t.Fatalf("zero jitter max delay=%v", got)
	}
}
