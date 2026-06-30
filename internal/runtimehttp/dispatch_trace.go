package runtimehttp

import (
	"context"
	"fmt"
	"strings"
)

type dispatchTraceContextKey struct{}

type dispatchTrace struct {
	rootEventID    string
	currentEventID string
	correlationID  string
	depth          int
	publishCount   int
	edges          map[string]struct{}
}

func newDispatchTrace() *dispatchTrace {
	return &dispatchTrace{edges: map[string]struct{}{}}
}

func dispatchTraceFromContext(ctx context.Context) *dispatchTrace {
	trace, _ := ctx.Value(dispatchTraceContextKey{}).(*dispatchTrace)
	return trace
}

func (t *dispatchTrace) enter() error {
	t.depth++
	if t.depth > dispatchMaxDepth {
		return fmt.Errorf("%w: depth %d exceeds max %d", errEventDepthExceeded, t.depth, dispatchMaxDepth)
	}
	return nil
}

func (t *dispatchTrace) leave() {
	if t.depth > 0 {
		t.depth--
	}
}

func (t *dispatchTrace) recordPublish(handler string, topic string) error {
	t.publishCount++
	if t.publishCount > dispatchMaxPublishes {
		return fmt.Errorf("%w: publish count %d exceeds max %d", errEventPublishLimitExceeded, t.publishCount, dispatchMaxPublishes)
	}
	handler = strings.TrimSpace(handler)
	topic = strings.TrimSpace(topic)
	if handler == "" || topic == "" {
		return nil
	}
	edge := handler + "\x00" + topic
	if _, ok := t.edges[edge]; ok {
		return fmt.Errorf("%w: handler %q already published topic %q in this dispatch trace", errEventCycleDetected, handler, topic)
	}
	t.edges[edge] = struct{}{}
	return nil
}

func (t *dispatchTrace) setRootEventID(id string) {
	if t.rootEventID == "" {
		t.rootEventID = strings.TrimSpace(id)
	}
}
