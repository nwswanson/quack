package logformat

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Attr struct {
	Key   string
	Value string
}

type Event struct {
	Time       time.Time
	TimeText   string
	Level      string
	Source     string
	Site       string
	Version    int64
	Route      string
	Message    string
	StackTrace string
	Attrs      []Attr
}

func Format(event Event) string {
	timeText := event.TimeText
	if timeText == "" {
		eventTime := event.Time
		if eventTime.IsZero() {
			eventTime = time.Now()
		}
		timeText = eventTime.UTC().Format(time.RFC3339Nano)
	}
	level := strings.ToLower(strings.TrimSpace(event.Level))
	if level == "" {
		level = "info"
	}
	source := strings.TrimSpace(event.Source)
	if source == "" {
		source = "system"
	}
	site := strings.TrimSpace(event.Site)
	if site == "" {
		site = "-"
	}
	route := strings.TrimSpace(event.Route)
	if route == "" {
		route = "-"
	}
	stackTrace := event.StackTrace
	if stackTrace == "" {
		stackTrace = "-"
	}

	line := fmt.Sprintf("%s %s %s [%s@v%d %s] %s %s",
		timeText,
		level,
		source,
		site,
		event.Version,
		route,
		QuoteField(event.Message),
		QuoteField(stackTrace),
	)
	if len(event.Attrs) > 0 {
		attrs := make([]string, 0, len(event.Attrs))
		for _, attr := range event.Attrs {
			key := strings.TrimSpace(attr.Key)
			if key == "" {
				continue
			}
			attrs = append(attrs, key+"="+QuoteField(attr.Value))
		}
		if len(attrs) > 0 {
			line += " " + strings.Join(attrs, " ")
		}
	}
	return line
}

func AttrsFromMap(values map[string]string) []Attr {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := make([]Attr, 0, len(keys))
	for _, key := range keys {
		attrs = append(attrs, Attr{Key: key, Value: values[key]})
	}
	return attrs
}

func QuoteField(value string) string {
	if value == "" {
		return "-"
	}
	if strings.ContainsAny(value, " \t\n\r\"") {
		return strconv.Quote(value)
	}
	return value
}

func NewSlogLogger(out io.Writer, level *slog.LevelVar) *slog.Logger {
	if out == nil {
		out = io.Discard
	}
	return slog.New(&slogHandler{out: out, level: level})
}

type slogHandler struct {
	out    io.Writer
	level  *slog.LevelVar
	attrs  []slog.Attr
	groups []string
	mu     sync.Mutex
}

func (h *slogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	min := slog.LevelWarn
	if h.level != nil {
		min = h.level.Level()
	}
	return level >= min
}

func (h *slogHandler) Handle(ctx context.Context, record slog.Record) error {
	event := Event{
		Time:    record.Time,
		Level:   record.Level.String(),
		Source:  sourceForRecord(record.Message),
		Message: record.Message,
	}
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
				event.Source = value
			}
		case "site":
			event.Site = value
		case "version":
			if version, err := strconv.ParseInt(value, 10, 64); err == nil {
				event.Version = version
			}
		case "route":
			event.Route = value
		case "message":
			event.Message = value
		case "stacktrace", "stack_trace", "backtrace":
			event.StackTrace = value
		default:
			event.Attrs = append(event.Attrs, Attr{Key: key, Value: value})
		}
	}
	for _, attr := range h.attrs {
		addAttr(attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addAttr(attr)
		return true
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, Format(event)+"\n")
	return err
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	next.mu = sync.Mutex{}
	return &next
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if strings.TrimSpace(name) == "" {
		return h
	}
	next := *h
	next.groups = append(append([]string(nil), h.groups...), name)
	next.mu = sync.Mutex{}
	return &next
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
