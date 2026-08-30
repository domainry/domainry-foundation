package worker

import (
	cryptorand "crypto/rand"
	"errors"
	"math/big"
	"strings"
	"time"
)

var ErrInvalidWorkerID = errors.New("worker id is required")

// WorkerID is the stable identity of one worker process. Business
// owners choose the identity; persistence adapters store it with each claim.
type WorkerID string

func NewWorkerID(value string) (WorkerID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrInvalidWorkerID
	}
	return WorkerID(value), nil
}

func (id WorkerID) String() string { return string(id) }

// FencingToken is a monotonically increasing attempt version allocated by a
// durable store. A token is scoped to the task identified by its business owner.
type FencingToken int64

func (token FencingToken) Valid() bool { return token > 0 }

// Lease contains only the technical ownership facts shared by durable workers.
// It deliberately does not define runnable, retryable, or terminal business state.
type Lease struct {
	Owner     WorkerID     `json:"owner"`
	Token     FencingToken `json:"fencing_token"`
	ExpiresAt time.Time    `json:"expires_at"`
}

func (lease Lease) Valid() bool {
	return strings.TrimSpace(lease.Owner.String()) != "" && lease.Token.Valid() && !lease.ExpiresAt.IsZero()
}

func (lease Lease) LiveAt(now time.Time) bool { return lease.Valid() && lease.ExpiresAt.After(now) }

func (lease Lease) Matches(owner WorkerID, token FencingToken) bool {
	return lease.Valid() && strings.TrimSpace(owner.String()) != "" && lease.Owner == owner && lease.Token == token
}

type HeartbeatState string

const (
	HeartbeatRenewed   HeartbeatState = "renewed"
	HeartbeatLeaseLost HeartbeatState = "lease_lost"
)

// HeartbeatResult reports the durable outcome of a heartbeat. Persistence
// errors remain Go errors; LeaseLost is a successful compare-and-swap with no
// matching current owner and token.
type HeartbeatResult struct {
	State HeartbeatState `json:"state"`
	Lease Lease          `json:"lease"`
}

func (result HeartbeatResult) Lost() bool { return result.State == HeartbeatLeaseLost }

type ShutdownState string

const (
	ShutdownRunning  ShutdownState = "running"
	ShutdownPaused   ShutdownState = "paused"
	ShutdownDraining ShutdownState = "draining"
	ShutdownStopped  ShutdownState = "stopped"
)

func (state ShutdownState) AcceptsClaims() bool { return state == ShutdownRunning }

// Clock and JitterSource are injected technical dependencies. They keep time
// and retry randomness out of owner state machines and deterministic tests.
type Clock interface {
	Now() time.Time
}

type JitterSource interface {
	Duration(max time.Duration) time.Duration
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type RandomJitterSource struct{}

func (RandomJitterSource) Duration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}

// Dependencies is the complete technical dependency bundle injected into a
// worker owner by its composition root. It contains no task status or retry policy.
type Dependencies struct {
	Clock    Clock
	WorkerID WorkerID
	Jitter   JitterSource
	Control  *Controller
	IDs      IdentifierGenerator
	Random   RandomSource
	Faults   FaultInjector
}

func NewDependencies(workerID WorkerID) Dependencies {
	return Dependencies{Clock: SystemClock{}, WorkerID: workerID, Jitter: RandomJitterSource{}, Control: NewController(), IDs: CryptoIdentifierGenerator{}, Random: CryptoRandomSource{}, Faults: NoopFaultInjector{}}
}

func (dependencies Dependencies) Valid() bool {
	return dependencies.Clock != nil && strings.TrimSpace(dependencies.WorkerID.String()) != "" && dependencies.Jitter != nil && dependencies.Control != nil && dependencies.IDs != nil && dependencies.Random != nil && dependencies.Faults != nil
}

func NormalizeDependencies(dependencies Dependencies) Dependencies {
	if dependencies.Clock == nil {
		dependencies.Clock = SystemClock{}
	}
	if strings.TrimSpace(dependencies.WorkerID.String()) == "" {
		dependencies.WorkerID = WorkerID("standalone-worker")
	}
	if dependencies.Jitter == nil {
		dependencies.Jitter = RandomJitterSource{}
	}
	if dependencies.Control == nil {
		dependencies.Control = NewController()
	}
	if dependencies.IDs == nil {
		dependencies.IDs = CryptoIdentifierGenerator{}
	}
	if dependencies.Random == nil {
		dependencies.Random = CryptoRandomSource{}
	}
	if dependencies.Faults == nil {
		dependencies.Faults = NoopFaultInjector{}
	}
	return dependencies
}
