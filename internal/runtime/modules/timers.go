package modules

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

func NewTimersModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "timers", Members: starlark.StringDict{
		"after":  starlark.NewBuiltin("timers.after", timerAfter),
		"at":     starlark.NewBuiltin("timers.at", timerAt),
		"every":  starlark.NewBuiltin("timers.every", timerEvery),
		"cancel": starlark.NewBuiltin("timers.cancel", timerCancel),
		"set":    starlark.NewBuiltin("timers.set", timerSet),
	}}
}

func timerAfter(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var msValue starlark.Int
	var topic, key string
	var mode = "new"
	var payload starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "ms", &msValue, "topic", &topic, "payload?", &payload, "key?", &key, "mode?", &mode); err != nil {
		return nil, err
	}
	ms, err := timerInt64(fn.Name(), "ms", msValue)
	if err != nil {
		return nil, err
	}
	if ms < 0 {
		return nil, fmt.Errorf("%s: ms must be non-negative", fn.Name())
	}
	return timerEffect("timers.after", topic, payload, key, mode, map[string]starlark.Value{
		"ms": starlark.MakeInt64(ms),
	})
}

func timerAt(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var unixMSValue starlark.Int
	var topic, key string
	var mode = "new"
	var payload starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "unix_ms", &unixMSValue, "topic", &topic, "payload?", &payload, "key?", &key, "mode?", &mode); err != nil {
		return nil, err
	}
	unixMS, err := timerInt64(fn.Name(), "unix_ms", unixMSValue)
	if err != nil {
		return nil, err
	}
	if unixMS < 0 {
		return nil, fmt.Errorf("%s: unix_ms must be non-negative", fn.Name())
	}
	return timerEffect("timers.at", topic, payload, key, mode, map[string]starlark.Value{
		"unix_ms": starlark.MakeInt64(unixMS),
	})
}

func timerEvery(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var msValue, jitterMSValue starlark.Int
	var topic, key string
	var payload starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "ms", &msValue, "topic", &topic, "payload?", &payload, "key", &key, "jitter_ms?", &jitterMSValue); err != nil {
		return nil, err
	}
	ms, err := timerInt64(fn.Name(), "ms", msValue)
	if err != nil {
		return nil, err
	}
	jitterMS, err := timerInt64(fn.Name(), "jitter_ms", jitterMSValue)
	if err != nil {
		return nil, err
	}
	if ms <= 0 {
		return nil, fmt.Errorf("%s: ms must be positive", fn.Name())
	}
	if jitterMS < 0 {
		return nil, fmt.Errorf("%s: jitter_ms must be non-negative", fn.Name())
	}
	return timerEffect("timers.every", topic, payload, key, "replace", map[string]starlark.Value{
		"ms":        starlark.MakeInt64(ms),
		"jitter_ms": starlark.MakeInt64(jitterMS),
	})
}

func timerCancel(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key, id string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key?", &key, "id?", &id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" && strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%s: key or id is required", fn.Name())
	}
	out := starlark.NewDict(3)
	_ = out.SetKey(starlark.String("type"), starlark.String("timers.cancel"))
	if key != "" {
		_ = out.SetKey(starlark.String("key"), starlark.String(key))
	}
	if id != "" {
		_ = out.SetKey(starlark.String("id"), starlark.String(id))
	}
	return out, nil
}

func timerSet(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var key, after string
	var event starlark.Value = starlark.None
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "key", &key, "after", &after, "event?", &event); err != nil {
		return nil, err
	}
	out := starlark.NewDict(5)
	_ = out.SetKey(starlark.String("type"), starlark.String("timers.set"))
	_ = out.SetKey(starlark.String("key"), starlark.String(key))
	_ = out.SetKey(starlark.String("after"), starlark.String(after))
	if event != starlark.None {
		_ = out.SetKey(starlark.String("payload"), event)
	}
	return out, nil
}

func timerEffect(effectType string, topic string, payload starlark.Value, key string, mode string, fields map[string]starlark.Value) (starlark.Value, error) {
	if strings.TrimSpace(topic) == "" {
		return nil, fmt.Errorf("%s requires topic", effectType)
	}
	out := starlark.NewDict(8)
	_ = out.SetKey(starlark.String("type"), starlark.String(effectType))
	_ = out.SetKey(starlark.String("id"), starlark.String(newTimerID()))
	_ = out.SetKey(starlark.String("topic"), starlark.String(topic))
	if payload != starlark.None {
		_ = out.SetKey(starlark.String("payload"), payload)
	}
	if key != "" {
		_ = out.SetKey(starlark.String("key"), starlark.String(key))
	}
	if mode != "" {
		_ = out.SetKey(starlark.String("mode"), starlark.String(mode))
	}
	for key, value := range fields {
		_ = out.SetKey(starlark.String(key), value)
	}
	return out, nil
}

func newTimerID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "tmr_fallback"
	}
	return "tmr_" + base64.RawURLEncoding.EncodeToString(b[:])
}

func timerInt64(fnName string, name string, value starlark.Int) (int64, error) {
	n, ok := value.Int64()
	if !ok {
		return 0, fmt.Errorf("%s: %s must fit int64", fnName, name)
	}
	return n, nil
}
