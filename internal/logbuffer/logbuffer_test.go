package logbuffer

import (
	"context"
	"testing"
	"time"
)

func TestServiceKeepsBoundedTail(t *testing.T) {
	logs := New(2)
	logs.Add(Event{Site: "a", Message: "one"})
	logs.Add(Event{Site: "a", Message: "two"})
	logs.Add(Event{Site: "a", Message: "three"})

	events := logs.Tail(Filter{Site: "a"}, 0)
	if len(events) != 2 || events[0].Message != "two" || events[1].Message != "three" {
		t.Fatalf("tail = %#v, want last two events", events)
	}
}

func TestServiceFiltersSiteAndSystem(t *testing.T) {
	logs := New(10)
	logs.Add(Event{Source: "access", Message: "system"})
	logs.Add(Event{Source: "starlark", Site: "alpha", Message: "alpha"})
	logs.Add(Event{Source: "starlark", Site: "beta", Message: "beta"})

	if events := logs.Tail(Filter{Site: "alpha"}, 0); len(events) != 1 || events[0].Message != "alpha" {
		t.Fatalf("site tail = %#v, want alpha only", events)
	}
	if events := logs.Tail(Filter{}, 0); len(events) != 2 {
		t.Fatalf("default tail len = %d, want site-only events", len(events))
	}
	if events := logs.Tail(Filter{IncludeSystem: true}, 0); len(events) != 3 {
		t.Fatalf("system tail len = %d, want all events", len(events))
	}
}

func TestServiceSubscribeReceivesMatchingEvents(t *testing.T) {
	logs := New(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := logs.Subscribe(ctx, Filter{Site: "alpha"})

	logs.Add(Event{Site: "beta", Message: "skip"})
	logs.Add(Event{Site: "alpha", Message: "keep"})

	select {
	case event := <-ch:
		if event.Message != "keep" {
			t.Fatalf("event = %#v, want keep", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscribed event")
	}
}
