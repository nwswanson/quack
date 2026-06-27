package runtime

import (
	"context"
	"os"
	"strings"
	"testing"

	"quack/internal/runtime/modules"
)

func TestDemoEventPipesLabExecutes(t *testing.T) {
	src, err := os.ReadFile("../../demos/event-pipes-lab/api/pipes.star")
	if err != nil {
		t.Fatal(err)
	}
	modules.WipeMemorySite("demo-event-pipes-lab")
	executor := newTestStarlarkExecutor(t, map[string]string{"api/pipes.star": string(src)})
	bundle := Bundle{
		Site:    "demo-event-pipes-lab",
		Version: 1,
		Routes:  []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "api/pipes.star"}},
	}

	effects, err := executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-event-pipes-lab", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectSend || !strings.Contains(string(effects[0].Payload), `"type":"ready"`) {
		t.Fatalf("connect effects = %+v, want ready send", effects)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-event-pipes-lab", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"start","flow":"map_reduce","session":"sabc","input":"pipe pipe event"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 3 || effects[0].Type != WebSocketEffectSubscribe || effects[0].Topic != "pipe-demo.session.sabc.trace" {
		t.Fatalf("start effects = %+v, want session trace subscription and publish", effects)
	}

	runMapReduce(t, executor, bundle)
	runScatterGather(t, executor, bundle)
	runSharding(t, executor, bundle)
}

func runMapReduce(t *testing.T, executor *StarlarkExecutor, bundle Bundle) {
	t.Helper()
	effects, err := executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_map_reduce_event",
		Topic: "pipe-demo.map_reduce.smr.start", Payload: []byte(`{"input":"pipe pipe event"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.map_reduce.smr.split", "") {
		t.Fatalf("map start effects = %+v, want split publish", effects)
	}

	effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_map_reduce_event",
		Topic: "pipe-demo.map_reduce.smr.split", Payload: []byte(`{"text":"pipe pipe event"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.map_reduce.smr.map", `"pipe"`) {
		t.Fatalf("map split effects = %+v, want map publish", effects)
	}

	effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_map_reduce_event",
		Topic: "pipe-demo.map_reduce.smr.map", Payload: []byte(`{"chunk":1,"words":["pipe","pipe","event"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.map_reduce.smr.reduce", `"pairs"`) {
		t.Fatalf("map worker effects = %+v, want reduce publish", effects)
	}
}

func runScatterGather(t *testing.T, executor *StarlarkExecutor, bundle Bundle) {
	t.Helper()
	effects, err := executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_scatter_gather_event",
		Topic: "pipe-demo.scatter_gather.ssg.start", Payload: []byte(`{"input":"alpha, websocket"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.scatter_gather.ssg.worker", `"alpha"`) {
		t.Fatalf("scatter start effects = %+v, want worker publish", effects)
	}

	_, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_scatter_gather_event",
		Topic: "pipe-demo.scatter_gather.ssg.worker", Payload: []byte(`{"index":1,"item":"alpha"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_scatter_gather_event",
		Topic: "pipe-demo.scatter_gather.ssg.worker", Payload: []byte(`{"index":2,"item":"websocket"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.scatter_gather.ssg.gather", `"responses"`) {
		t.Fatalf("scatter worker effects = %+v, want gather publish", effects)
	}
}

func runSharding(t *testing.T, executor *StarlarkExecutor, bundle Bundle) {
	t.Helper()
	effects, err := executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_sharding_event",
		Topic: "pipe-demo.sharding.ssh.start", Payload: []byte(`{"input":"ada:4\nben:7","shards":2}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.sharding.ssh.route", `"records"`) {
		t.Fatalf("sharding start effects = %+v, want route publish", effects)
	}

	effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/pipes.star", Handler: "on_sharding_event",
		Topic: "pipe-demo.sharding.ssh.route", Payload: []byte(`{"records":[{"key":"ada","value":4},{"key":"ben","value":7}],"shards":2}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.sharding.ssh.shard", `"ada"`) {
		t.Fatalf("sharding route effects = %+v, want shard publish", effects)
	}
}

func hasPublish(effects []WebSocketEffect, topic string, payloadFragment string) bool {
	for _, effect := range effects {
		if effect.Type != WebSocketEffectPublish || effect.Topic != topic {
			continue
		}
		if payloadFragment == "" || strings.Contains(string(effect.Payload), payloadFragment) {
			return true
		}
	}
	return false
}
