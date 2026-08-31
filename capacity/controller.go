package capacity

import (
	"context"
	"strings"
	"sync"
	"time"
)

type counter struct {
	inFlight   int
	rate       int
	windowFrom time.Time
	lastSeen   time.Time
}

type Controller struct {
	mu         sync.Mutex
	limits     Limits
	global     int
	globalRate counter
	workspaces map[string]counter
	useCases   map[string]counter
	retries    int
	state      State
	rejected   uint64
	recovered  uint64
	now        func() time.Time
	events     EventSink
}

type Lease struct {
	once       sync.Once
	controller *Controller
	request    Request
}

func NewController(limits Limits, events EventSink) *Controller {
	limits = normalizeLimits(limits)
	return &Controller{limits: limits, workspaces: map[string]counter{}, useCases: map[string]counter{}, state: Normal, now: time.Now, events: events}
}

func (c *Controller) Acquire(ctx context.Context, request Request) (*Lease, Decision) {
	if err := ctx.Err(); err != nil {
		return nil, Decision{Allowed: false, State: c.State(), Code: "capacity.request_cancelled"}
	}
	request.WorkspaceID = boundedKey(request.WorkspaceID, "")
	if request.WorkspaceID == "" || strings.EqualFold(request.WorkspaceID, "default") {
		return nil, Decision{Allowed: false, State: c.State(), Code: "capacity.workspace_scope_required"}
	}
	request.UseCase = boundedKey(request.UseCase, "unknown")
	c.mu.Lock()
	now := c.now().UTC()
	c.evictIdle(now)
	c.resetRateWindows(now, request)
	decision := c.admit(request, now)
	if decision.Allowed {
		c.global++
		c.globalRate.rate++
		c.globalRate.lastSeen = now
		workspace := c.workspaces[request.WorkspaceID]
		resetCounterWindow(&workspace, now, c.limits.RateWindow)
		workspace.inFlight++
		workspace.rate++
		workspace.lastSeen = now
		c.workspaces[request.WorkspaceID] = workspace
		useCase := c.useCases[request.UseCase]
		resetCounterWindow(&useCase, now, c.limits.RateWindow)
		useCase.inFlight++
		useCase.rate++
		useCase.lastSeen = now
		c.useCases[request.UseCase] = useCase
		if request.Retry {
			c.retries++
		}
		c.updateStateLocked()
		decision.State = c.state
	}
	event := c.eventFor(decision, request, now)
	c.mu.Unlock()
	if event.Code != "" && c.events != nil {
		c.events(event)
	}
	if !decision.Allowed {
		return nil, decision
	}
	return &Lease{controller: c, request: request}, decision
}

func (l *Lease) Release() {
	if l == nil || l.controller == nil {
		return
	}
	l.once.Do(func() { l.controller.release(l.request) })
}

func (c *Controller) release(request Request) {
	c.mu.Lock()
	if c.global > 0 {
		c.global--
	}
	decrement(c.workspaces, request.WorkspaceID)
	decrement(c.useCases, request.UseCase)
	if request.Retry && c.retries > 0 {
		c.retries--
	}
	previous := c.state
	c.updateStateLocked()
	if previous != Normal && c.state == Normal {
		c.recovered++
	}
	state, global, limit, sink, now := c.state, c.global, c.limits.GlobalInFlight, c.events, c.now().UTC()
	c.mu.Unlock()
	if previous != state && state == Normal && sink != nil {
		sink(Event{State: state, Dimension: DimensionProcess, Current: global, Limit: limit, Code: "capacity.recovered", At: now})
	}
}

func (c *Controller) State() State { c.mu.Lock(); defer c.mu.Unlock(); return c.state }

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{State: c.state, GlobalInFlight: c.global, GlobalLimit: c.limits.GlobalInFlight, GlobalRate: c.globalRate.rate, GlobalRateLimit: c.limits.GlobalRate, WorkspaceStates: len(c.workspaces), UseCaseStates: len(c.useCases), RejectedTotal: c.rejected, RecoveredTotal: c.recovered}
}

func (c *Controller) admit(request Request, now time.Time) Decision {
	base := Decision{Allowed: true, State: c.state, RetryAfter: c.limits.RetryAfter}
	if c.state == Degraded && !request.Essential {
		return c.reject(base, DimensionProcess, c.global, c.limits.GlobalInFlight, "capacity.nonessential_shed", request, now)
	}
	if c.global >= c.limits.GlobalInFlight {
		return c.reject(base, DimensionProcess, c.global, c.limits.GlobalInFlight, "capacity.process_overloaded", request, now)
	}
	if c.globalRate.rate >= c.limits.GlobalRate {
		return c.reject(base, DimensionProcess, c.globalRate.rate, c.limits.GlobalRate, "capacity.process_rate_exceeded", request, now)
	}
	workspace := c.workspaces[request.WorkspaceID]
	if workspace.inFlight >= c.limits.WorkspaceInFlight {
		return c.reject(base, DimensionWorkspace, workspace.inFlight, c.limits.WorkspaceInFlight, "capacity.workspace_overloaded", request, now)
	}
	if workspace.rate >= c.limits.WorkspaceRate {
		return c.reject(base, DimensionWorkspace, workspace.rate, c.limits.WorkspaceRate, "capacity.workspace_rate_exceeded", request, now)
	}
	useCase := c.useCases[request.UseCase]
	if useCase.inFlight >= c.limits.UseCaseInFlight {
		return c.reject(base, DimensionUseCase, useCase.inFlight, c.limits.UseCaseInFlight, "capacity.use_case_overloaded", request, now)
	}
	if useCase.rate >= c.limits.UseCaseRate {
		return c.reject(base, DimensionUseCase, useCase.rate, c.limits.UseCaseRate, "capacity.use_case_rate_exceeded", request, now)
	}
	if request.Retry && c.retries >= c.limits.RetryInFlight {
		return c.reject(base, DimensionRetry, c.retries, c.limits.RetryInFlight, "capacity.retry_budget_exhausted", request, now)
	}
	if _, exists := c.workspaces[request.WorkspaceID]; !exists && len(c.workspaces) >= c.limits.MaxWorkspaceStates {
		return c.reject(base, DimensionWorkspace, len(c.workspaces), c.limits.MaxWorkspaceStates, "capacity.workspace_state_exhausted", request, now)
	}
	if _, exists := c.useCases[request.UseCase]; !exists && len(c.useCases) >= c.limits.MaxUseCaseStates {
		return c.reject(base, DimensionUseCase, len(c.useCases), c.limits.MaxUseCaseStates, "capacity.use_case_state_exhausted", request, now)
	}
	return base
}

func (c *Controller) resetRateWindows(now time.Time, request Request) {
	resetCounterWindow(&c.globalRate, now, c.limits.RateWindow)
	if workspace, exists := c.workspaces[request.WorkspaceID]; exists {
		resetCounterWindow(&workspace, now, c.limits.RateWindow)
		c.workspaces[request.WorkspaceID] = workspace
	}
	if useCase, exists := c.useCases[request.UseCase]; exists {
		resetCounterWindow(&useCase, now, c.limits.RateWindow)
		c.useCases[request.UseCase] = useCase
	}
}

func resetCounterWindow(value *counter, now time.Time, window time.Duration) {
	if value.windowFrom.IsZero() || now.Sub(value.windowFrom) >= window {
		value.rate = 0
		value.windowFrom = now
	}
}

func (c *Controller) reject(decision Decision, dimension Dimension, current, limit int, code string, _ Request, _ time.Time) Decision {
	c.rejected++
	decision.Allowed, decision.Dimension, decision.Current, decision.Limit, decision.Code = false, dimension, current, limit, code
	if code == "capacity.process_overloaded" {
		c.state = Overloaded
		decision.State = Overloaded
	}
	return decision
}

func (c *Controller) updateStateLocked() {
	ratio := float64(c.global) / float64(c.limits.GlobalInFlight)
	switch c.state {
	case Overloaded, Degraded:
		if ratio <= c.limits.RecoveryRatio {
			c.state = Normal
		} else if ratio < 1 {
			c.state = Degraded
		}
	default:
		if ratio >= 1 {
			c.state = Overloaded
		} else if ratio >= c.limits.DegradedRatio {
			c.state = Degraded
		}
	}
}

func (c *Controller) evictIdle(now time.Time) {
	for key, value := range c.workspaces {
		if value.inFlight == 0 && now.Sub(value.lastSeen) >= c.limits.WorkspaceStateTTL {
			delete(c.workspaces, key)
		}
	}
	for key, value := range c.useCases {
		if value.inFlight == 0 && now.Sub(value.lastSeen) >= c.limits.WorkspaceStateTTL {
			delete(c.useCases, key)
		}
	}
}
func (c *Controller) eventFor(decision Decision, request Request, now time.Time) Event {
	if decision.Allowed {
		return Event{}
	}
	return Event{State: decision.State, Dimension: decision.Dimension, WorkspaceID: request.WorkspaceID, UseCase: request.UseCase, Current: decision.Current, Limit: decision.Limit, Code: decision.Code, At: now}
}
func decrement(values map[string]counter, key string) {
	value := values[key]
	if value.inFlight > 0 {
		value.inFlight--
	}
	values[key] = value
}
func boundedKey(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 128 {
		return fallback
	}
	return value
}
func normalizeLimits(value Limits) Limits {
	if value.GlobalInFlight <= 0 {
		value.GlobalInFlight = 256
	}
	if value.WorkspaceInFlight <= 0 {
		value.WorkspaceInFlight = 32
	}
	if value.UseCaseInFlight <= 0 {
		value.UseCaseInFlight = 64
	}
	if value.RetryInFlight <= 0 {
		value.RetryInFlight = 16
	}
	if value.GlobalRate <= 0 {
		value.GlobalRate = 6000
	}
	if value.WorkspaceRate <= 0 {
		value.WorkspaceRate = 600
	}
	if value.UseCaseRate <= 0 {
		value.UseCaseRate = 1200
	}
	if value.RateWindow <= 0 {
		value.RateWindow = time.Minute
	}
	if value.MaxWorkspaceStates <= 0 {
		value.MaxWorkspaceStates = 10_000
	}
	if value.MaxUseCaseStates <= 0 {
		value.MaxUseCaseStates = 1024
	}
	if value.WorkspaceStateTTL <= 0 {
		value.WorkspaceStateTTL = 30 * time.Minute
	}
	if value.DegradedRatio <= 0 || value.DegradedRatio >= 1 {
		value.DegradedRatio = .8
	}
	if value.RecoveryRatio <= 0 || value.RecoveryRatio >= value.DegradedRatio {
		value.RecoveryRatio = .6
	}
	if value.RetryAfter <= 0 {
		value.RetryAfter = 2 * time.Second
	}
	return value
}
