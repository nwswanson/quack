package modules

import (
	"fmt"

	"go.starlark.net/starlark"
)

const effectCollectorThreadLocal = "quack.effects"

type EffectCollector struct {
	effects []starlark.Value
}

func InstallEffectCollector(thread *starlark.Thread) *EffectCollector {
	collector := &EffectCollector{}
	thread.SetLocal(effectCollectorThreadLocal, collector)
	return collector
}

func QueueEffect(thread *starlark.Thread, fnName string, effect starlark.Value) error {
	collector, ok := thread.Local(effectCollectorThreadLocal).(*EffectCollector)
	if !ok || collector == nil {
		return fmt.Errorf("%s can only be used inside websocket/event handlers", fnName)
	}
	collector.effects = append(collector.effects, effect)
	return nil
}

func (c *EffectCollector) Effects() []starlark.Value {
	if c == nil || len(c.effects) == 0 {
		return nil
	}
	return append([]starlark.Value(nil), c.effects...)
}
