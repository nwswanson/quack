package runtimehttp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"quack/internal/eventpipe"
	"quack/internal/logbuffer"
	appruntime "quack/internal/runtime"
)

const runtimeTraceSource = "runtime_trace"

type traceRecord struct {
	Site    string
	Version int64
	Route   string
	Message string
	Level   string
	Attrs   map[string]string
}

func (h Handler) addTrace(record traceRecord) {
	if h.logs == nil {
		return
	}
	if record.Level == "" {
		record.Level = "info"
	}
	if record.Attrs == nil {
		record.Attrs = map[string]string{}
	}
	h.logs.Add(logbuffer.Event{
		Level:      record.Level,
		Source:     runtimeTraceSource,
		Site:       record.Site,
		Version:    record.Version,
		Route:      record.Route,
		Message:    record.Message,
		Attributes: record.Attrs,
	})
}

func baseTraceAttrs(trace *dispatchTrace, event eventpipe.Event) map[string]string {
	attrs := map[string]string{
		"span_id":        randomSpanID(),
		"trace_id":       traceID(trace, event),
		"event_id":       event.ID,
		"event_type":     event.Type,
		"event_subject":  event.Topic,
		"event_source":   event.Source,
		"pipe":           event.Pipe,
		"seq":            strconv.FormatUint(event.Seq, 10),
		"correlation_id": event.CorrelationID,
		"causation_id":   event.CausationID,
		"root_event_id":  traceRootEventID(trace, event),
	}
	if !event.Time.IsZero() {
		attrs["event_time"] = event.Time.Format(time.RFC3339Nano)
	}
	if event.ActionID != "" {
		attrs["action_id"] = event.ActionID
	}
	return attrs
}

func traceID(trace *dispatchTrace, event eventpipe.Event) string {
	if trace != nil && strings.TrimSpace(trace.correlationID) != "" {
		return trace.correlationID
	}
	if event.CorrelationID != "" {
		return event.CorrelationID
	}
	if event.ID != "" {
		return event.ID
	}
	return randomSpanID()
}

func traceRootEventID(trace *dispatchTrace, event eventpipe.Event) string {
	if trace != nil && trace.rootEventID != "" {
		return trace.rootEventID
	}
	return event.ID
}

func effectTraceAttrs(effect appruntime.WebSocketEffect) map[string]string {
	attrs := map[string]string{
		"span_id":     randomSpanID(),
		"effect_type": string(effect.Type),
	}
	if effect.ConnID != "" {
		attrs["conn_id"] = effect.ConnID
	}
	if effect.Topic != "" {
		attrs["topic"] = effect.Topic
	}
	if effect.Key != "" {
		attrs["key"] = effect.Key
	}
	if effect.ID != "" {
		attrs["timer_id"] = effect.ID
	}
	if effect.ActionID != "" {
		attrs["action_id"] = effect.ActionID
	}
	if len(effect.Payload) > 0 {
		attrs["payload_bytes"] = strconv.Itoa(len(effect.Payload))
	}
	return attrs
}

func effectList(effects []appruntime.WebSocketEffect) string {
	if len(effects) == 0 {
		return "[]"
	}
	items := make([]map[string]string, 0, len(effects))
	for _, effect := range effects {
		item := map[string]string{"type": string(effect.Type)}
		if effect.Topic != "" {
			item["topic"] = effect.Topic
		}
		if effect.ConnID != "" {
			item["conn_id"] = effect.ConnID
		}
		if effect.Key != "" {
			item["key"] = effect.Key
		}
		if effect.ID != "" {
			item["id"] = effect.ID
		}
		if effect.ActionID != "" {
			item["action_id"] = effect.ActionID
		}
		items = append(items, item)
	}
	data, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func durationMillis(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return strconv.FormatInt(d.Milliseconds(), 10)
}

func randomSpanID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("span-%d", time.Now().UnixNano())
	}
	return "span_" + base64.RawURLEncoding.EncodeToString(b[:])
}
