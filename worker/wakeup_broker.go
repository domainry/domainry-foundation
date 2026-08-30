package worker

import (
	"strings"
	"sync"
)

type DurableTaskLocator struct {
	QueueKind   string
	WorkspaceID string
	TaskID      string
}

// WakeupBroker is a process-local, lossy fan-out for committed durable work.
// It never owns work: subscribers must still claim the owner row and retain a
// durable recovery query for dropped or cross-process notifications.
type WakeupBroker struct {
	mu          sync.RWMutex
	subscribers map[string][]chan DurableTaskLocator
}

func NewWakeupBroker() *WakeupBroker {
	return &WakeupBroker{subscribers: map[string][]chan DurableTaskLocator{}}
}

func (b *WakeupBroker) Subscribe(queueKind string, capacity int) <-chan DurableTaskLocator {
	if capacity <= 0 {
		capacity = 1
	}
	channel := make(chan DurableTaskLocator, capacity)
	if b == nil || strings.TrimSpace(queueKind) == "" {
		return channel
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	queueKind = strings.TrimSpace(queueKind)
	b.subscribers[queueKind] = append(b.subscribers[queueKind], channel)
	return channel
}

func (b *WakeupBroker) Publish(locator DurableTaskLocator) {
	if b == nil {
		return
	}
	locator.QueueKind = strings.TrimSpace(locator.QueueKind)
	locator.WorkspaceID = strings.TrimSpace(locator.WorkspaceID)
	locator.TaskID = strings.TrimSpace(locator.TaskID)
	if locator.QueueKind == "" || locator.WorkspaceID == "" || locator.TaskID == "" {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, subscriber := range b.subscribers[locator.QueueKind] {
		select {
		case subscriber <- locator:
		default:
		}
	}
}
