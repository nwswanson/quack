package runtime

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"quack/internal/eventpipe"
)

const pipeStressScript = `
SHARDS = 8
STEPS = 3

def _emit(topic, payload):
    events.publish(topic, payload)

def start(ctx, event):
    payload = event.payload
    for shard in range(SHARDS):
        _emit("bench.pipe.shard.%d" % shard, {
            "session": payload["session"],
            "shard": shard,
            "step": 0,
            "data": payload["data"],
        })

def shard(ctx, event):
    payload = event.payload
    session = payload["session"]
    shard_id = int(payload["shard"])
    step = int(payload["step"])
    data = payload["data"]
    memory.incr("bench.count.%s.%d" % (session, shard_id), len(data) + step)
    if step + 1 < STEPS:
        _emit("bench.pipe.shard.%d" % shard_id, {
            "session": session,
            "shard": shard_id,
            "step": step + 1,
            "data": data + "|%d" % step,
        })
        return
    _emit("bench.pipe.reduce", {
        "session": session,
        "shard": shard_id,
        "value": memory.get("bench.count.%s.%d" % (session, shard_id), 0),
    })

def reduce(ctx, event):
    payload = event.payload
    session = payload["session"]
    count = memory.incr("bench.done.%s" % session, 1)
    memory.list_push("bench.values.%s" % session, payload["value"])
    if count == SHARDS:
        values = memory.list_range("bench.values.%s" % session, 0, -1)
        total = 0
        for value in values:
            total += value
        return _emit("bench.pipe.done", {"session": session, "total": total})
    return None
`

func BenchmarkStarlarkPipeFlow(b *testing.B) {
	bench := newPipeFlowBench(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := bench.run(context.Background(), i); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStarlarkPipeFlowParallel(b *testing.B) {
	bench := newPipeFlowBench(b)
	if err := bench.run(context.Background(), -1); err != nil {
		b.Fatal(err)
	}
	var seq atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id := int(seq.Add(1))
			if err := bench.run(context.Background(), id); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func newPipeFlowBench(b *testing.B) pipeFlowBench {
	b.Helper()
	executor, err := NewStarlarkExecutor(ScriptLoaderFunc(func(ctx context.Context, objectKey string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(pipeStressScript)), nil
	}), ResourceLimits{})
	if err != nil {
		b.Fatal(err)
	}
	return pipeFlowBench{
		executor: executor,
		store:    eventpipe.NewStore(),
		retain:   256,
	}
}

type pipeFlowBench struct {
	executor *StarlarkExecutor
	store    *eventpipe.Store
	retain   int
}

func (p pipeFlowBench) run(ctx context.Context, seq int) error {
	return p.invoke(ctx, "start", "bench.pipe.start", []byte(`{"session":"s`+itoa(seq)+`","data":"abcdefghijklmnopqrstuvwxyz0123456789"}`))
}

func (p pipeFlowBench) invoke(ctx context.Context, handler string, topic string, payload []byte) error {
	config := eventpipe.Config{Name: topic, Retain: p.retain}
	event, ok := p.store.Publish(config, eventpipe.Event{
		Site:       "bench",
		Pipe:       config.Name,
		Topic:      topic,
		SourceKind: "benchmark",
		SourceName: handler,
		Payload:    payload,
	})
	if !ok {
		return nil
	}

	effects, err := p.executor.InvokeEvent(ctx, Bundle{
		Site:    "bench",
		Version: 1,
		Routes:  []Route{{Path: "event:bench.star", Kind: RouteWebSocket, Entrypoint: "bench.star"}},
		Files:   []BundleFile{{Path: "bench.star", BlobPath: "bench.star", FileSHA: "bench-pipe-script", Bytes: int64(len(pipeStressScript))}},
	}, EventInvocation{
		Site:       "bench",
		Version:    1,
		Entrypoint: "bench.star",
		Handler:    handler,
		Topic:      event.Topic,
		Payload:    event.Payload,
	})
	if err != nil {
		return err
	}

	for _, effect := range effects {
		if effect.Type != WebSocketEffectPublish {
			continue
		}
		next := handlerForTopic(effect.Topic)
		if next == "" {
			continue
		}
		if err := p.invoke(ctx, next, effect.Topic, effect.Payload); err != nil {
			return err
		}
	}
	return nil
}

func handlerForTopic(topic string) string {
	switch {
	case strings.HasPrefix(topic, "bench.pipe.shard."):
		return "shard"
	case topic == "bench.pipe.reduce":
		return "reduce"
	default:
		return ""
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
