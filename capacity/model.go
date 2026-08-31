package capacity

import "time"

type State string

const (
	Normal     State = "normal"
	Degraded   State = "degraded"
	Overloaded State = "overloaded"
)

type Dimension string

const (
	DimensionProcess   Dimension = "process"
	DimensionWorkspace Dimension = "workspace"
	DimensionUseCase   Dimension = "use_case"
	DimensionRetry     Dimension = "retry"
)

type Limits struct {
	GlobalInFlight     int
	WorkspaceInFlight  int
	UseCaseInFlight    int
	RetryInFlight      int
	GlobalRate         int
	WorkspaceRate      int
	UseCaseRate        int
	RateWindow         time.Duration
	MaxWorkspaceStates int
	MaxUseCaseStates   int
	WorkspaceStateTTL  time.Duration
	DegradedRatio      float64
	RecoveryRatio      float64
	RetryAfter         time.Duration
}

type Request struct {
	WorkspaceID string
	UseCase     string
	Retry       bool
	Essential   bool
}

type Decision struct {
	Allowed    bool
	State      State
	Dimension  Dimension
	Current    int
	Limit      int
	RetryAfter time.Duration
	Code       string
}

type Snapshot struct {
	State           State  `json:"state"`
	GlobalInFlight  int    `json:"global_in_flight"`
	GlobalLimit     int    `json:"global_limit"`
	GlobalRate      int    `json:"global_rate"`
	GlobalRateLimit int    `json:"global_rate_limit"`
	WorkspaceStates int    `json:"workspace_states"`
	UseCaseStates   int    `json:"use_case_states"`
	RejectedTotal   uint64 `json:"rejected_total"`
	RecoveredTotal  uint64 `json:"recovered_total"`
}

type Event struct {
	State       State
	Dimension   Dimension
	WorkspaceID string
	UseCase     string
	Current     int
	Limit       int
	Code        string
	At          time.Time
}

type EventSink func(Event)
