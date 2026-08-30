package worker

import "testing"

func TestWakeupBrokerRoutesExactBoundedDurableHints(t *testing.T) {
	broker := NewWakeupBroker()
	outbox := broker.Subscribe("integration_outbox", 1)
	events := broker.Subscribe("integration_event", 1)
	locator := DurableTaskLocator{QueueKind: "integration_outbox", WorkspaceID: "workspace", TaskID: "message"}
	broker.Publish(locator)
	broker.Publish(DurableTaskLocator{QueueKind: "integration_outbox", WorkspaceID: "workspace", TaskID: "dropped-when-full"})
	if received := <-outbox; received != locator {
		t.Fatalf("locator=%#v", received)
	}
	select {
	case received := <-events:
		t.Fatalf("cross-queue wakeup=%#v", received)
	default:
	}
}
