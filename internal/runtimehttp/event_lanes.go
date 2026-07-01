package runtimehttp

import (
	"context"
	"sync"
	"time"
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
	_, _, err := r.DoMeasured(ctx, key, fn)
	return err
}

func (r *eventLaneRegistry) DoMeasured(ctx context.Context, key string, fn func() error) (time.Duration, time.Duration, error) {
	lane := r.acquire(key)
	defer r.release(key, lane)

	waitStarted := time.Now()
	select {
	case <-ctx.Done():
		return time.Since(waitStarted), 0, ctx.Err()
	case <-lane.ch:
	}
	wait := time.Since(waitStarted)
	holdStarted := time.Now()
	defer func() { lane.ch <- struct{}{} }()
	err := fn()
	return wait, time.Since(holdStarted), err
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
