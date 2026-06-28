package runtime

import (
	"context"
	"os"
	"strings"
	"testing"

	"quack/internal/runtime/modules"
)

func TestDemoMultiRoomChatExecutes(t *testing.T) {
	src, err := os.ReadFile("../../demos/multi-room-chat/api/chat.star")
	if err != nil {
		t.Fatal(err)
	}
	modules.WipeMemorySite("demo-multi-room-chat")
	executor := newTestStarlarkExecutor(t, map[string]string{"api/chat.star": string(src)})
	bundle := Bundle{
		Site:    "demo-multi-room-chat",
		Version: 1,
		Routes:  []Route{{Path: "/ws", Kind: RouteWebSocket, Entrypoint: "api/chat.star"}},
	}

	effects, err := executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-multi-room-chat", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventConnect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 2 || effects[0].Type != WebSocketEffectSubscribe || effects[0].Topic != "chat.room.*" {
		t.Fatalf("connect effects = %+v, want wildcard subscription", effects)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-multi-room-chat", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"join","room":42,"name":"Ada"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSend(effects, `"chat.room.42"`) || !hasPublish(effects, "chat.room.42", `joined room 42`) {
		t.Fatalf("join effects = %+v, want joined send and room publish", effects)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-multi-room-chat", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"message","text":"hello selectors"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPublish(effects, "chat.room.42", `hello selectors`) {
		t.Fatalf("message effects = %+v, want concrete room publish", effects)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-multi-room-chat", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventEvent,
		Event: WebSocketServerEvent{Topic: "chat.room.42", Payload: []byte(`{"type":"message","room":42,"name":"Ada","text":"hello selectors"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSend(effects, `"topic":"chat.room.42"`) || !hasSend(effects, `"selector":"chat.room.*"`) {
		t.Fatalf("event effects = %+v, want delivered selector payload", effects)
	}

	effects, err = executor.InvokeWebSocket(context.Background(), bundle, WebSocketEvent{
		Site: "demo-multi-room-chat", Version: 1, Route: "/ws", ConnID: "c1", EventType: WebSocketEventMessage,
		Message: []byte(`{"type":"join","room":101,"name":"Ada"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSend(effects, `"room must be 1-100"`) {
		t.Fatalf("invalid join effects = %+v, want room bounds error", effects)
	}
}

func hasSend(effects []WebSocketEffect, payloadFragment string) bool {
	for _, effect := range effects {
		if effect.Type == WebSocketEffectSend && strings.Contains(string(effect.Payload), payloadFragment) {
			return true
		}
	}
	return false
}
