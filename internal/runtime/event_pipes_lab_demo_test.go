package runtime

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"quack/internal/runtime/modules"
)

func TestDemoEventPipesLabExecutes(t *testing.T) {
	pipesSrc, err := os.ReadFile("../../demos/event-pipes-lab/api/pipes.star")
	if err != nil {
		t.Fatal(err)
	}
	mapReduceSrc, err := os.ReadFile("../../demos/event-pipes-lab/api/map_reduce.star")
	if err != nil {
		t.Fatal(err)
	}
	scatterGatherSrc, err := os.ReadFile("../../demos/event-pipes-lab/api/scatter_gather.star")
	if err != nil {
		t.Fatal(err)
	}
	shardingSrc, err := os.ReadFile("../../demos/event-pipes-lab/api/sharding.star")
	if err != nil {
		t.Fatal(err)
	}
	modules.WipeMemorySite("demo-event-pipes-lab")
	executor := newTestStarlarkExecutor(t, map[string]string{
		"api/pipes.star":          string(pipesSrc),
		"api/map_reduce.star":     string(mapReduceSrc),
		"api/scatter_gather.star": string(scatterGatherSrc),
		"api/sharding.star":       string(shardingSrc),
	})
	socketBundle := Bundle{
		Site:    "demo-event-pipes-lab",
		Version: 1,
		Routes:  []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "api/pipes.star"}},
	}

	effects, err := executor.InvokeWebSocket(context.Background(), socketBundle, WebSocketEvent{
		Site: "demo-event-pipes-lab", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || effects[0].Type != WebSocketEffectSend || !strings.Contains(string(effects[0].Payload), `"type":"ready"`) {
		t.Fatalf("connect effects = %+v, want ready send", effects)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), socketBundle, WebSocketEvent{
		Site: "demo-event-pipes-lab", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"start","flow":"map_reduce","session":"sabc","input":"pipe pipe event"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 3 || effects[0].Type != WebSocketEffectSubscribe || effects[0].Topic != "pipe-demo.session.sabc.trace" {
		t.Fatalf("start effects = %+v, want session trace subscription and publish", effects)
	}

	runMapReduce(t, executor)
	runScatterGather(t, executor)
	runSharding(t, executor)
}

func runMapReduce(t *testing.T, executor *StarlarkExecutor) {
	t.Helper()
	bundle := eventBundle("api/map_reduce.star")
	effects, err := executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/map_reduce.star", Handler: "start_node",
		Topic: "pipe-demo.map_reduce.start", Payload: []byte(`{"session":"smr","input":"pipe pipe event"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.map_reduce.split", "") {
		t.Fatalf("map start effects = %+v, want split publish", effects)
	}

	effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/map_reduce.star", Handler: "split_node",
		Topic: "pipe-demo.map_reduce.split", Payload: []byte(`{"session":"smr","text":"pipe pipe event"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{
		"pipe-demo.map_reduce.map_0",
		"pipe-demo.map_reduce.map_1",
		"pipe-demo.map_reduce.map_2",
		"pipe-demo.map_reduce.map_3",
	} {
		if !hasPublish(effects, topic, `"session":"smr"`) {
			t.Fatalf("map split effects = %+v, want publish to %s", effects, topic)
		}
	}

	for i, step := range []struct {
		handler string
		topic   string
		words   string
	}{
		{"map_0_node", "pipe-demo.map_reduce.map_0", `["pipe"]`},
		{"map_1_node", "pipe-demo.map_reduce.map_1", `["pipe"]`},
		{"map_2_node", "pipe-demo.map_reduce.map_2", `["event"]`},
		{"map_3_node", "pipe-demo.map_reduce.map_3", `[]`},
	} {
		effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
			Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/map_reduce.star", Handler: step.handler,
			Topic: step.topic, Payload: []byte(`{"session":"smr","worker":` + strconv.Itoa(i) + `,"words":` + step.words + `}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !hasPublish(effects, "pipe-demo.map_reduce.reduce", `"pairs"`) {
		t.Fatalf("last map worker effects = %+v, want reduce publish", effects)
	}
}

func runScatterGather(t *testing.T, executor *StarlarkExecutor) {
	t.Helper()
	bundle := eventBundle("api/scatter_gather.star")
	effects, err := executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/scatter_gather.star", Handler: "start_node",
		Topic: "pipe-demo.scatter_gather.start", Payload: []byte(`{"session":"ssg","input":"alpha, websocket"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{
		"pipe-demo.scatter_gather.profile",
		"pipe-demo.scatter_gather.pricing",
		"pipe-demo.scatter_gather.inventory",
		"pipe-demo.scatter_gather.risk",
	} {
		if !hasPublish(effects, topic, `"alpha"`) {
			t.Fatalf("scatter start effects = %+v, want publish to %s", effects, topic)
		}
	}

	for _, step := range []struct {
		handler string
		topic   string
	}{
		{"profile_node", "pipe-demo.scatter_gather.profile"},
		{"pricing_node", "pipe-demo.scatter_gather.pricing"},
		{"inventory_node", "pipe-demo.scatter_gather.inventory"},
		{"risk_node", "pipe-demo.scatter_gather.risk"},
	} {
		effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
			Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/scatter_gather.star", Handler: step.handler,
			Topic: step.topic, Payload: []byte(`{"session":"ssg","items":["alpha","websocket"]}`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !hasPublish(effects, "pipe-demo.scatter_gather.gather", `"responses"`) {
		t.Fatalf("last scatter worker effects = %+v, want gather publish", effects)
	}
}

func runSharding(t *testing.T, executor *StarlarkExecutor) {
	t.Helper()
	bundle := eventBundle("api/sharding.star")
	effects, err := executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/sharding.star", Handler: "start_node",
		Topic: "pipe-demo.sharding.start", Payload: []byte(`{"session":"ssh","input":"ada:4\nben:7"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.sharding.route", `"records"`) {
		t.Fatalf("sharding start effects = %+v, want route publish", effects)
	}

	effects, err = executor.InvokeEvent(context.Background(), bundle, EventInvocation{
		Site: "demo-event-pipes-lab", Version: 1, Entrypoint: "api/sharding.star", Handler: "route_node",
		Topic: "pipe-demo.sharding.route", Payload: []byte(`{"session":"ssh","records":[{"key":"ada","value":4},{"key":"ben","value":7}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "pipe-demo.sharding.shard_0", "") &&
		!hasPublish(effects, "pipe-demo.sharding.shard_1", "") &&
		!hasPublish(effects, "pipe-demo.sharding.shard_2", "") &&
		!hasPublish(effects, "pipe-demo.sharding.shard_3", "") {
		t.Fatalf("sharding route effects = %+v, want shard publish", effects)
	}
}

func eventBundle(entrypoint string) Bundle {
	return Bundle{
		Site:    "demo-event-pipes-lab",
		Version: 1,
		Routes:  []Route{{Path: "event:" + entrypoint, Kind: RouteWebSocket, Entrypoint: entrypoint}},
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
