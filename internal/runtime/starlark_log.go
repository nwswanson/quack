package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"quack/internal/logbuffer"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

func (e *StarlarkExecutor) logModule(ctx context.Context, bundle Bundle, route Route) *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "log", Members: starlark.StringDict{
		"debug": starlark.NewBuiltin("log.debug", e.logBuiltin(ctx, "debug", bundle, route)),
		"info":  starlark.NewBuiltin("log.info", e.logBuiltin(ctx, "info", bundle, route)),
		"warn":  starlark.NewBuiltin("log.warn", e.logBuiltin(ctx, "warn", bundle, route)),
		"error": starlark.NewBuiltin("log.error", e.logBuiltin(ctx, "error", bundle, route)),
	}}
}

func (e *StarlarkExecutor) logBuiltin(ctx context.Context, level string, bundle Bundle, route Route) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%s: got %d arguments, want 1", fn.Name(), len(args))
		}
		message := starlarkValueString(args[0])
		attrs := map[string]string{}
		for _, kwarg := range kwargs {
			if len(kwarg) != 2 {
				continue
			}
			key, ok := starlark.AsString(kwarg[0])
			if !ok || strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("%s: keyword name must be string", fn.Name())
			}
			attrs[key] = starlarkValueString(kwarg[1])
		}
		if len(attrs) == 0 {
			attrs = nil
		}

		slogAttrs := []slog.Attr{
			slog.String("site", bundle.Site),
			slog.Int64("version", bundle.Version),
			slog.String("route", route.Path),
			slog.String("message", message),
		}
		for key, value := range attrs {
			slogAttrs = append(slogAttrs, slog.String(key, value))
		}
		slog.LogAttrs(ctx, slogLevel(level), "starlark log", slogAttrs...)

		if e.logs != nil {
			e.logs.Add(logbuffer.Event{
				Level:      level,
				Source:     "starlark",
				Site:       bundle.Site,
				Version:    bundle.Version,
				Route:      route.Path,
				Message:    message,
				Attributes: attrs,
			})
		}
		return starlark.None, nil
	}
}

func slogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func starlarkValueString(value starlark.Value) string {
	if s, ok := starlark.AsString(value); ok {
		return s
	}
	return value.String()
}
