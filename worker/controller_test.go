package worker

import (
	"testing"
	"time"
)

func TestControllerPauseResumeAndDrain(t *testing.T) {
	controller := NewController()
	if !controller.TryBegin() {
		t.Fatal("running controller rejected work")
	}
	if !controller.Pause() || controller.TryBegin() {
		t.Fatal("paused controller accepted work")
	}
	if controller.WaitIdle(time.Millisecond) {
		t.Fatal("controller reported idle with in-flight work")
	}
	controller.End()
	if !controller.WaitIdle(time.Millisecond) || !controller.Resume() || !controller.Drain() || controller.TryBegin() {
		t.Fatal("controller resume/drain contract failed")
	}
	if !controller.Undrain() || !controller.TryBegin() {
		t.Fatal("controller undrain contract failed")
	}
	controller.End()
	if !controller.Drain() {
		t.Fatal("controller second drain failed")
	}
	controller.Stop()
	if controller.Snapshot().State != ShutdownStopped {
		t.Fatal("controller did not stop")
	}
}
