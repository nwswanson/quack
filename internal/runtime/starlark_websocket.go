package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

func (e *StarlarkExecutor) InvokeWebSocket(ctx context.Context, bundle Bundle, event WebSocketEvent) ([]WebSocketEffect, error) {
	route, err := singleWebSocketRoute(bundle)
	if err != nil {
		return nil, err
	}
	limits := bundle.Limits.withFallback(e.limits)
	scriptKey := route.ScriptKey
	if scriptKey == "" {
		scriptKey = route.Entrypoint
	}
	script, err := e.readScript(ctx, scriptKey, limits)
	if err != nil {
		return nil, err
	}
	thread, stopCancel := starlarkThread(ctx, string(event.EventType)+" "+event.Route, limits.MaxExecutionSteps)
	defer stopCancel()
	globals, err := starlark.ExecFile(thread, route.Entrypoint, script, e.websocketPredeclareds(ctx, bundle, route, limits))
	if err != nil {
		return nil, e.wrapStarlarkError(bundle, route, err)
	}
	handler, args, err := websocketHandler(globals, event, route.Path)
	if err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, nil
	}
	globals.Freeze()
	result, err := starlark.Call(thread, handler, args, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrTimeout
		}
		return nil, e.wrapStarlarkError(bundle, route, err)
	}
	return websocketEffectsFromValue(result)
}

func (e *StarlarkExecutor) websocketPredeclareds(ctx context.Context, bundle Bundle, route Route, limits ResourceLimits) starlark.StringDict {
	out := e.predeclareds(ctx, bundle, route, limits)
	out["ws"] = websocketModule()
	out["events"] = eventsModule()
	out["timers"] = timersModule()
	return out
}

func websocketHandler(globals starlark.StringDict, event WebSocketEvent, routePath string) (starlark.Callable, starlark.Tuple, error) {
	var name string
	var args starlark.Tuple
	ctx := websocketContext(event, routePath)
	switch event.EventType {
	case WebSocketEventConnect:
		name = "on_connect"
		args = starlark.Tuple{ctx}
	case WebSocketEventMessage:
		name = "on_message"
		args = starlark.Tuple{ctx, websocketPayloadValue(event.Message)}
	case WebSocketEventDisconnect:
		name = "on_disconnect"
		args = starlark.Tuple{ctx}
	case WebSocketEventEvent, WebSocketEventTimer:
		name = "on_event"
		args = starlark.Tuple{ctx, websocketServerEventValue(event.Event)}
	default:
		return nil, nil, fmt.Errorf("%w: unsupported websocket event type %s", ErrInvalidRuntime, event.EventType)
	}
	value, ok := globals[name]
	if !ok {
		return nil, nil, nil
	}
	callable, ok := value.(starlark.Callable)
	if !ok {
		return nil, nil, fmt.Errorf("%w: %s must be callable", ErrInvalidRuntime, name)
	}
	return callable, args, nil
}

func websocketContext(event WebSocketEvent, routePath string) starlark.Value {
	headers := starlark.NewDict(len(event.Headers))
	for key, values := range event.Headers {
		_ = headers.SetKey(starlark.String(strings.ToLower(key)), starlark.NewList(stringValues(values)))
	}
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"site":    starlark.String(event.Site),
		"version": starlark.MakeInt64(event.Version),
		"route":   starlark.String(routePath),
		"path":    starlark.String(pathUnderRoute(event.Route, routePath)),
		"query":   starlark.String(event.Query),
		"headers": headers,
		"conn_id": starlark.String(event.ConnID),
		"params":  starlark.NewDict(0),
		"user": starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
			"id": starlark.String("anonymous"),
		}),
	})
}

func websocketPayloadValue(payload []byte) starlark.Value {
	if len(payload) == 0 {
		return starlark.None
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err == nil {
		return starlarkValueFromAny(decoded)
	}
	return starlark.String(string(payload))
}

func websocketServerEventValue(event WebSocketServerEvent) starlark.Value {
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"topic":   starlark.String(event.Topic),
		"payload": websocketPayloadValue(event.Payload),
	})
}

func websocketEffectsFromValue(v starlark.Value) ([]WebSocketEffect, error) {
	if v == starlark.None {
		return nil, nil
	}
	if list, ok := v.(*starlark.List); ok {
		out := make([]WebSocketEffect, 0, list.Len())
		iter := list.Iterate()
		defer iter.Done()
		var value starlark.Value
		for iter.Next(&value) {
			effect, err := websocketEffectFromValue(value)
			if err != nil {
				return nil, err
			}
			out = append(out, effect)
		}
		return out, nil
	}
	if tuple, ok := v.(starlark.Tuple); ok {
		out := make([]WebSocketEffect, 0, tuple.Len())
		for _, value := range tuple {
			effect, err := websocketEffectFromValue(value)
			if err != nil {
				return nil, err
			}
			out = append(out, effect)
		}
		return out, nil
	}
	effect, err := websocketEffectFromValue(v)
	if err != nil {
		return nil, err
	}
	return []WebSocketEffect{effect}, nil
}

func websocketEffectFromValue(v starlark.Value) (WebSocketEffect, error) {
	dict, ok := v.(*starlark.Dict)
	if !ok {
		return WebSocketEffect{}, fmt.Errorf("%w: websocket effect must be dict", ErrInvocationFailure)
	}
	effectType, err := dictString(dict, "type")
	if err != nil {
		return WebSocketEffect{}, err
	}
	effect := WebSocketEffect{Type: WebSocketEffectType(effectType)}
	effect.ConnID, _ = dictOptionalString(dict, "conn_id")
	effect.Topic, _ = dictOptionalString(dict, "topic")
	effect.Key, _ = dictOptionalString(dict, "key")
	effect.After, _ = dictOptionalString(dict, "after")
	effect.Reason, _ = dictOptionalString(dict, "reason")
	if code, ok, err := dictOptionalInt(dict, "code"); err != nil {
		return WebSocketEffect{}, err
	} else if ok {
		effect.Code = code
	}
	if payload, ok, err := dictPayloadBytes(dict, "payload"); err != nil {
		return WebSocketEffect{}, err
	} else if ok {
		effect.Payload = payload
	}
	switch effect.Type {
	case WebSocketEffectAccept, WebSocketEffectUnsubscribeAll:
	case WebSocketEffectClose:
	case WebSocketEffectSend:
		if effect.ConnID == "" {
			return WebSocketEffect{}, fmt.Errorf("%w: ws.send requires conn_id", ErrInvocationFailure)
		}
	case WebSocketEffectBroadcast, WebSocketEffectPublish:
		if effect.Topic == "" {
			return WebSocketEffect{}, fmt.Errorf("%w: %s requires topic", ErrInvocationFailure, effect.Type)
		}
	case WebSocketEffectSubscribe, WebSocketEffectUnsubscribe:
		if effect.ConnID == "" || effect.Topic == "" {
			return WebSocketEffect{}, fmt.Errorf("%w: %s requires conn_id and topic", ErrInvocationFailure, effect.Type)
		}
	case WebSocketEffectSetTimer:
		if effect.Key == "" || effect.After == "" {
			return WebSocketEffect{}, fmt.Errorf("%w: timers.set requires key and after", ErrInvocationFailure)
		}
	default:
		return WebSocketEffect{}, fmt.Errorf("%w: unknown websocket effect %s", ErrInvocationFailure, effect.Type)
	}
	return effect, nil
}

func websocketModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "ws", Members: starlark.StringDict{
		"accept":          starlark.NewBuiltin("ws.accept", makeEffectBuiltin(WebSocketEffectAccept, nil)),
		"close":           starlark.NewBuiltin("ws.close", makeEffectBuiltin(WebSocketEffectClose, []string{"conn_id?", "code?", "reason?"})),
		"send":            starlark.NewBuiltin("ws.send", makeEffectBuiltin(WebSocketEffectSend, []string{"conn_id", "payload"})),
		"broadcast":       starlark.NewBuiltin("ws.broadcast", makeEffectBuiltin(WebSocketEffectBroadcast, []string{"topic", "payload"})),
		"subscribe":       starlark.NewBuiltin("ws.subscribe", makeEffectBuiltin(WebSocketEffectSubscribe, []string{"conn_id", "topic"})),
		"unsubscribe":     starlark.NewBuiltin("ws.unsubscribe", makeEffectBuiltin(WebSocketEffectUnsubscribe, []string{"conn_id", "topic"})),
		"unsubscribe_all": starlark.NewBuiltin("ws.unsubscribe_all", makeEffectBuiltin(WebSocketEffectUnsubscribeAll, []string{"conn_id"})),
	}}
}

func eventsModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "events", Members: starlark.StringDict{
		"publish": starlark.NewBuiltin("events.publish", makeEffectBuiltin(WebSocketEffectPublish, []string{"topic", "payload"})),
	}}
}

func timersModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "timers", Members: starlark.StringDict{
		"set": starlark.NewBuiltin("timers.set", makeEffectBuiltin(WebSocketEffectSetTimer, []string{"key", "after", "event?"})),
	}}
}

func makeEffectBuiltin(effectType WebSocketEffectType, fields []string) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var connID, topic, key, after, reason string
		var code int
		var payload starlark.Value = starlark.None
		var event starlark.Value = starlark.None
		targets := []any{}
		for _, field := range fields {
			switch field {
			case "conn_id", "conn_id?":
				targets = append(targets, field, &connID)
			case "topic":
				targets = append(targets, field, &topic)
			case "key":
				targets = append(targets, field, &key)
			case "after":
				targets = append(targets, field, &after)
			case "reason?":
				targets = append(targets, field, &reason)
			case "code?":
				targets = append(targets, field, &code)
			case "payload":
				targets = append(targets, field, &payload)
			case "event?":
				targets = append(targets, field, &event)
			}
		}
		if err := starlark.UnpackArgs(fn.Name(), args, kwargs, targets...); err != nil {
			return nil, err
		}
		out := starlark.NewDict(6)
		_ = out.SetKey(starlark.String("type"), starlark.String(effectType))
		if connID != "" {
			_ = out.SetKey(starlark.String("conn_id"), starlark.String(connID))
		}
		if topic != "" {
			_ = out.SetKey(starlark.String("topic"), starlark.String(topic))
		}
		if key != "" {
			_ = out.SetKey(starlark.String("key"), starlark.String(key))
		}
		if after != "" {
			_ = out.SetKey(starlark.String("after"), starlark.String(after))
		}
		if reason != "" {
			_ = out.SetKey(starlark.String("reason"), starlark.String(reason))
		}
		if code != 0 {
			_ = out.SetKey(starlark.String("code"), starlark.MakeInt(code))
		}
		if payload != starlark.None {
			_ = out.SetKey(starlark.String("payload"), payload)
		}
		if event != starlark.None {
			_ = out.SetKey(starlark.String("payload"), event)
		}
		return out, nil
	}
}

func dictString(dict *starlark.Dict, key string) (string, error) {
	value, ok, err := dict.Get(starlark.String(key))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: effect missing %s", ErrInvocationFailure, key)
	}
	s, ok := starlark.AsString(value)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%w: effect %s must be string", ErrInvocationFailure, key)
	}
	return s, nil
}

func dictOptionalString(dict *starlark.Dict, key string) (string, bool) {
	value, ok, err := dict.Get(starlark.String(key))
	if err != nil || !ok {
		return "", false
	}
	s, ok := starlark.AsString(value)
	return s, ok
}

func dictOptionalInt(dict *starlark.Dict, key string) (int, bool, error) {
	value, ok, err := dict.Get(starlark.String(key))
	if err != nil || !ok {
		return 0, false, err
	}
	n, err := starlark.AsInt32(value)
	if err != nil {
		return 0, false, fmt.Errorf("%w: effect %s must be int", ErrInvocationFailure, key)
	}
	return int(n), true, nil
}

func dictPayloadBytes(dict *starlark.Dict, key string) ([]byte, bool, error) {
	value, ok, err := dict.Get(starlark.String(key))
	if err != nil || !ok {
		return nil, false, err
	}
	payload, err := starlarkPayloadBytes(value)
	return payload, true, err
}

func starlarkPayloadBytes(value starlark.Value) ([]byte, error) {
	switch v := value.(type) {
	case starlark.Bytes:
		return []byte(string(v)), nil
	case starlark.String:
		return []byte(string(v)), nil
	}
	goValue, err := anyFromStarlark(value)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(goValue)
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not JSON-encodable: %v", ErrInvocationFailure, err)
	}
	return data, nil
}

func starlarkValueFromAny(v any) starlark.Value {
	switch value := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(value)
	case string:
		return starlark.String(value)
	case float64:
		return starlark.Float(value)
	case []any:
		out := make([]starlark.Value, 0, len(value))
		for _, item := range value {
			out = append(out, starlarkValueFromAny(item))
		}
		return starlark.NewList(out)
	case map[string]any:
		out := starlark.NewDict(len(value))
		for key, item := range value {
			_ = out.SetKey(starlark.String(key), starlarkValueFromAny(item))
		}
		return out
	default:
		return starlark.String(fmt.Sprint(value))
	}
}

func anyFromStarlark(v starlark.Value) (any, error) {
	switch value := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(value), nil
	case starlark.String:
		return string(value), nil
	case starlark.Bytes:
		return string(value), nil
	case starlark.Int:
		if n, ok := value.Int64(); ok {
			return n, nil
		}
		return nil, fmt.Errorf("%w: integer is too large for JSON payload", ErrInvocationFailure)
	case starlark.Float:
		return float64(value), nil
	case *starlark.List:
		out := make([]any, 0, value.Len())
		iter := value.Iterate()
		defer iter.Done()
		var item starlark.Value
		for iter.Next(&item) {
			goItem, err := anyFromStarlark(item)
			if err != nil {
				return nil, err
			}
			out = append(out, goItem)
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, 0, value.Len())
		for _, item := range value {
			goItem, err := anyFromStarlark(item)
			if err != nil {
				return nil, err
			}
			out = append(out, goItem)
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]any, value.Len())
		for _, item := range value.Items() {
			key, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("%w: payload dict keys must be strings", ErrInvocationFailure)
			}
			goItem, err := anyFromStarlark(item[1])
			if err != nil {
				return nil, err
			}
			out[key] = goItem
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: unsupported payload value %s", ErrInvocationFailure, v.Type())
	}
}

func singleWebSocketRoute(bundle Bundle) (Route, error) {
	if len(bundle.Routes) != 1 {
		return Route{}, fmt.Errorf("%w: bundle must contain exactly one WebSocket route", ErrInvalidRuntime)
	}
	if route := bundle.Routes[0]; route.Kind == RouteWebSocket {
		return route, nil
	}
	return Route{}, fmt.Errorf("%w: expected WebSocket route", ErrInvalidRuntime)
}
