package runtimehttp

import (
	"context"
	"sync"
)

type eventLaneRegistry struct {
	mu    sync.Mutex
	lanes map[string]*eventLane
}

type eventLane struct {
	ch   chan struct{}
	refs int
}

func newEventLaneRegistry() *eventLaneRegistry {
	return &eventLaneRegistry{lanes: map[string]*eventLane{}}
}

func (r *eventLaneRegistry) Do(ctx context.Context, key string, fn func() error) error {
	lane := r.acquire(key)
	defer r.release(key, lane)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lane.ch:
	}
	defer func() { lane.ch <- struct{}{} }()
	return fn()
}

func (r *eventLaneRegistry) acquire(key string) *eventLane {
	r.mu.Lock()
	defer r.mu.Unlock()
	lane := r.lanes[key]
	if lane == nil {
		lane = &eventLane{ch: make(chan struct{}, 1)}
		lane.ch <- struct{}{}
		r.lanes[key] = lane
	}
	lane.refs++
	return lane
}

func (r *eventLaneRegistry) release(key string, lane *eventLane) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lane.refs--
	if lane.refs == 0 {
		delete(r.lanes, key)
	}
}
