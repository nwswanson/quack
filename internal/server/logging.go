package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

var processLogLevel slog.LevelVar

func init() {
	processLogLevel.Set(slog.LevelWarn)
}

func ConfigureLogger(value string, out io.Writer) error {
	if err := SetLogLevel(value); err != nil {
		return err
	}
	slog.SetDefault(NewLogger(out))
	return nil
}

func NewLogger(out io.Writer) *slog.Logger {
	return NewLoggerWithLevel(out, &processLogLevel)
}

func NewLoggerWithLevel(out io.Writer, level *slog.LevelVar) *slog.Logger {
	if out == nil {
		out = io.Discard
	}
	return slog.New(&stdoutHandler{out: out, level: level})
}

func SetLogLevel(value string) error {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return fmt.Errorf("unknown level %q; expected debug, info, warn, or error", value)
	}
	processLogLevel.Set(level)
	return nil
}

type stdoutHandler struct {
	out    io.Writer
	level  *slog.LevelVar
	attrs  []slog.Attr
	groups []string
	mu     sync.Mutex
}

func (h *stdoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	min := slog.LevelWarn
	if h.level != nil {
		min = h.level.Level()
	}
	return level >= min
}

func (h *stdoutHandler) Handle(ctx context.Context, record slog.Record) error {
	fields := logFields{
		Time:       record.Time,
		Level:      strings.ToLower(record.Level.String()),
		Source:     sourceForRecord(record.Message),
		Message:    record.Message,
		Site:       "-",
		Version:    "-",
		Route:      "-",
		StackTrace: "-",
	}
	attrs := make([]string, 0, record.NumAttrs()+len(h.attrs))
	addAttr := func(attr slog.Attr) {
		attr.Value = attr.Value.Resolve()
		key := strings.Join(append(append([]string(nil), h.groups...), attr.Key), ".")
		if key == "" {
			return
		}
		value := valueString(attr.Value)
		switch key {
		case "source":
			if value != "" {
				fields.Source = value
			}
		case "site":
			if value != "" {
				fields.Site = value
			}
		case "version":
			if value != "" && value != "0" {
				fields.Version = "v" + value
			}
		case "route":
			if value != "" {
				fields.Route = value
			}
		case "message":
			fields.Message = value
		case "stacktrace", "stack_trace", "backtrace":
			if value != "" {
				fields.StackTrace = value
			}
		default:
			attrs = append(attrs, key+"="+quoteLogField(value))
		}
	}
	for _, attr := range h.attrs {
		addAttr(attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addAttr(attr)
		return true
	})
	if fields.Version == "-" {
		fields.Version = "v0"
	}
	line := fmt.Sprintf("%s %s %s [%s@%s %s] %s %s",
		fields.Time.UTC().Format(time.RFC3339Nano),
		fields.Level,
		fields.Source,
		fields.Site,
		fields.Version,
		fields.Route,
		quoteLogField(fields.Message),
		quoteLogField(fields.StackTrace),
	)
	if len(attrs) > 0 {
		line += " " + strings.Join(attrs, " ")
	}
	line += "\n"
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, line)
	return err
}

func (h *stdoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	next.mu = sync.Mutex{}
	return &next
}

func (h *stdoutHandler) WithGroup(name string) slog.Handler {
	if strings.TrimSpace(name) == "" {
		return h
	}
	next := *h
	next.groups = append(append([]string(nil), h.groups...), name)
	next.mu = sync.Mutex{}
	return &next
}

type logFields struct {
	Time       time.Time
	Level      string
	Source     string
	Site       string
	Version    string
	Route      string
	Message    string
	StackTrace string
}

func sourceForRecord(message string) string {
	switch message {
	case "starlark log":
		return "starlark"
	case "starlark invocation failed":
		return "starlark_error"
	case "http request":
		return "access"
	default:
		return "system"
	}
}

func valueString(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindInt64:
		return strconv.FormatInt(value.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(value.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(value.Float64(), 'f', -1, 64)
	case slog.KindBool:
		return strconv.FormatBool(value.Bool())
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(value.Any())
	}
}

func quoteLogField(value string) string {
	if value == "" {
		return "-"
	}
	if strings.ContainsAny(value, " \t\n\r\"") {
		return strconv.Quote(value)
	}
	return value
}
