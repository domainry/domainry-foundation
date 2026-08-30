package worker

import (
	"fmt"
	"sync"
	"time"
)

// Controller coordinates only process-local claim admission and drain. It does
// not own business task state; durable Lease/Fencing still decides execution.
type Controller struct {
	mu       sync.Mutex
	state    ShutdownState
	inFlight int
	idle     chan struct{}
}

type ControllerSnapshot struct {
	State    ShutdownState `json:"state"`
	InFlight int           `json:"in_flight"`
}

func (controller *Controller) OpenMetrics() string {
	snapshot := controller.Snapshot()
	return fmt.Sprintf("# HELP domainry_runtime_worker_controller_state Process worker claim admission state (1=current).\n# TYPE domainry_runtime_worker_controller_state gauge\ndomainry_runtime_worker_controller_state{state=\"%s\"} 1\n# HELP domainry_runtime_worker_controller_in_flight Worker ticks currently executing.\n# TYPE domainry_runtime_worker_controller_in_flight gauge\ndomainry_runtime_worker_controller_in_flight %d\n", snapshot.State, snapshot.InFlight)
}

func NewController() *Controller {
	idle := make(chan struct{})
	close(idle)
	return &Controller{state: ShutdownRunning, idle: idle}
}

func (controller *Controller) Snapshot() ControllerSnapshot {
	if controller == nil {
		return ControllerSnapshot{State: ShutdownStopped}
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return ControllerSnapshot{State: controller.state, InFlight: controller.inFlight}
}

func (controller *Controller) Pause() bool {
	return controller.transition(ShutdownRunning, ShutdownPaused)
}

func (controller *Controller) Resume() bool {
	return controller.transition(ShutdownPaused, ShutdownRunning)
}

func (controller *Controller) Undrain() bool {
	return controller.transition(ShutdownDraining, ShutdownRunning)
}

func (controller *Controller) Drain() bool {
	if controller == nil {
		return false
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state == ShutdownStopped || controller.state == ShutdownDraining {
		return false
	}
	controller.state = ShutdownDraining
	return true
}

func (controller *Controller) Stop() {
	if controller == nil {
		return
	}
	controller.mu.Lock()
	controller.state = ShutdownStopped
	controller.mu.Unlock()
}

func (controller *Controller) TryBegin() bool {
	if controller == nil {
		return true
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if !controller.state.AcceptsClaims() {
		return false
	}
	if controller.inFlight == 0 {
		controller.idle = make(chan struct{})
	}
	controller.inFlight++
	return true
}

func (controller *Controller) End() {
	if controller == nil {
		return
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.inFlight == 0 {
		return
	}
	controller.inFlight--
	if controller.inFlight == 0 {
		close(controller.idle)
	}
}

func (controller *Controller) RunIfAccepting(run func()) bool {
	if run == nil || !controller.TryBegin() {
		return false
	}
	defer controller.End()
	run()
	return true
}

func (controller *Controller) WaitIdle(timeout time.Duration) bool {
	if controller == nil {
		return true
	}
	controller.mu.Lock()
	idle := controller.idle
	controller.mu.Unlock()
	if timeout <= 0 {
		select {
		case <-idle:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-idle:
		return true
	case <-timer.C:
		return false
	}
}

func (controller *Controller) transition(from, to ShutdownState) bool {
	if controller == nil {
		return false
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state != from {
		return false
	}
	controller.state = to
	return true
}
