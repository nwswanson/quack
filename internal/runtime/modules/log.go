package modules

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"quack/internal/logbuffer"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type LogModuleOptions struct {
	Buffer  *logbuffer.Service
	Site    string
	Version int64
	Route   string
}

func NewLogModule(ctx context.Context, opts LogModuleOptions) *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "log", Members: starlark.StringDict{
		"debug": starlark.NewBuiltin("log.debug", logBuiltin(ctx, "debug", opts)),
		"info":  starlark.NewBuiltin("log.info", logBuiltin(ctx, "info", opts)),
		"warn":  starlark.NewBuiltin("log.warn", logBuiltin(ctx, "warn", opts)),
		"error": starlark.NewBuiltin("log.error", logBuiltin(ctx, "error", opts)),
	}}
}

func logBuiltin(ctx context.Context, level string, opts LogModuleOptions) func(*starlark.Thread, *starlark.Builtin, starlark.Tuple, []starlark.Tuple) (starlark.Value, error) {
	return func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		message, attrs, err := logMessageAndAttrs(fn.Name(), args, kwargs)
		if err != nil {
			return nil, err
		}
		if len(attrs) == 0 {
			attrs = nil
		}

		slogAttrs := []slog.Attr{
			slog.String("site", opts.Site),
			slog.Int64("version", opts.Version),
			slog.String("route", opts.Route),
			slog.String("message", message),
		}
		for key, value := range attrs {
			slogAttrs = append(slogAttrs, slog.String(key, value))
		}
		slog.LogAttrs(ctx, slogLevel(level), "starlark log", slogAttrs...)

		if opts.Buffer != nil {
			opts.Buffer.Add(logbuffer.Event{
				Level:      level,
				Source:     "starlark",
				Site:       opts.Site,
				Version:    opts.Version,
				Route:      opts.Route,
				Message:    message,
				Attributes: attrs,
			})
		}
		return starlark.None, nil
	}
}

func logMessageAndAttrs(fnName string, args starlark.Tuple, kwargs []starlark.Tuple) (string, map[string]string, error) {
	parts := make([]string, 0, len(args)+1)
	attrs := map[string]string{}
	for _, kwarg := range kwargs {
		if len(kwarg) != 2 {
			continue
		}
		key, ok := starlark.AsString(kwarg[0])
		if !ok || strings.TrimSpace(key) == "" {
			return "", nil, fmt.Errorf("%s: keyword name must be string", fnName)
		}
		value := starlarkValueString(kwarg[1])
		if key == "message" {
			if value != "" {
				parts = append(parts, value)
			}
			continue
		}
		attrs[key] = value
	}
	for _, arg := range args {
		parts = append(parts, starlarkValueString(arg))
	}
	return strings.Join(parts, " "), attrs, nil
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
