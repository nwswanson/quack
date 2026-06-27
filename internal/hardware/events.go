package hardware

import (
	"context"
	"sync"
)

type hardwareEventQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	maxItems int
	events   []HardwareEvent
	closed   bool
	dropped  int64
}

func newHardwareEventQueue(maxItems int) *hardwareEventQueue {
	if maxItems <= 0 {
		maxItems = 1024
	}
	q := &hardwareEventQueue{maxItems: maxItems}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *hardwareEventQueue) publish(event HardwareEvent) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	if len(q.events) >= q.maxItems {
		dropped := q.events[0]
		q.events = append([]HardwareEvent(nil), q.events[1:]...)
		q.dropped++
		if event.DroppedEvents == 0 {
			event.DroppedEvents = q.dropped
		}
		if event.DroppedBytes == 0 {
			event.DroppedBytes = int64(len(dropped.Bytes))
		}
	}
	q.events = append(q.events, cloneHardwareEvent(event))
	q.cond.Signal()
}

func (q *hardwareEventQueue) watch(ctx context.Context) <-chan HardwareEvent {
	out := make(chan HardwareEvent)
	go func() {
		defer close(out)
		go func() {
			<-ctx.Done()
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		}()
		for {
			q.mu.Lock()
			for len(q.events) == 0 && !q.closed && ctx.Err() == nil {
				q.cond.Wait()
			}
			if ctx.Err() != nil || q.closed {
				q.mu.Unlock()
				return
			}
			event := q.events[0]
			q.events = append([]HardwareEvent(nil), q.events[1:]...)
			q.mu.Unlock()
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (q *hardwareEventQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

func cloneHardwareEvent(event HardwareEvent) HardwareEvent {
	event.Bytes = append([]byte(nil), event.Bytes...)
	return event
}
