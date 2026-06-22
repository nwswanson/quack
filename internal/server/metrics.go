package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"quack/internal/domain"
	appruntime "quack/internal/runtime"
	"quack/internal/runtime/modules"
	"quack/internal/runtimehttp"
)

type metricsRepository interface {
	MetricsSnapshot(ctx context.Context) (domain.MetricsSnapshot, error)
}

type prometheusRuntimeStats struct {
	runtime runtimehttp.Handler
}

func (s prometheusRuntimeStats) activeWebSocketsTotal() int64 {
	return s.runtime.ActiveWebSocketsTotal()
}

func (s prometheusRuntimeStats) activeWebSocketsBySite() map[string]int64 {
	return s.runtime.ActiveWebSocketsBySite()
}

type prometheusMetrics struct {
	bootTime time.Time
	db       metricsRepository
	runtime  prometheusRuntimeStats

	mu                  sync.Mutex
	httpRequests        map[httpRequestKey]uint64
	httpRequestDuration map[httpRequestKey]float64
	runtimeInvocations  map[runtimeInvocationKey]uint64
	runtimeDuration     map[runtimeInvocationKey]float64
	runtimeErrors       map[runtimeErrorKey]uint64
	metricsScrapeErrors uint64
}

type httpRequestKey struct {
	surface string
	method  string
	status  int
}

type runtimeInvocationKey struct {
	site        string
	route       string
	runtimeKind string
	result      string
}

type runtimeErrorKey struct {
	site  string
	route string
	kind  string
}

func newPrometheusMetrics(db metricsRepository, runtime runtimehttp.Handler) *prometheusMetrics {
	return &prometheusMetrics{
		bootTime:            time.Now(),
		db:                  db,
		runtime:             prometheusRuntimeStats{runtime: runtime},
		httpRequests:        map[httpRequestKey]uint64{},
		httpRequestDuration: map[httpRequestKey]float64{},
		runtimeInvocations:  map[runtimeInvocationKey]uint64{},
		runtimeDuration:     map[runtimeInvocationKey]float64{},
		runtimeErrors:       map[runtimeErrorKey]uint64{},
	}
}

func (m *prometheusMetrics) RecordHTTPRequest(surface string, method string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if surface == "" {
		surface = "unknown"
	}
	key := httpRequestKey{surface: surface, method: method, status: status}
	m.mu.Lock()
	m.httpRequests[key]++
	m.httpRequestDuration[key] += duration.Seconds()
	m.mu.Unlock()
}

func (m *prometheusMetrics) RecordInvocation(ctx context.Context, event appruntime.InvocationEvent) {
	if m == nil {
		return
	}
	result := "success"
	if event.Error != "" {
		result = "error"
	}
	key := runtimeInvocationKey{
		site:        event.Site,
		route:       event.Route,
		runtimeKind: string(event.RuntimeKind),
		result:      result,
	}
	m.mu.Lock()
	m.runtimeInvocations[key]++
	m.runtimeDuration[key] += event.Duration.Seconds()
	if event.ErrorKind != "" {
		m.runtimeErrors[runtimeErrorKey{site: event.Site, route: event.Route, kind: event.ErrorKind}]++
	}
	m.mu.Unlock()
}

func (m *prometheusMetrics) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writer := newPrometheusWriter(w)
	m.collect(r.Context(), writer)
}

func (m *prometheusMetrics) collect(ctx context.Context, w *prometheusWriter) {
	w.metric("quack_up", "gauge", "Whether the quack process is serving metrics.", nil, 1)
	w.metric("quack_boot_time_seconds", "gauge", "Unix timestamp for when this process metrics registry was created.", nil, float64(m.bootTime.Unix()))

	m.mu.Lock()
	httpRequests := cloneMap(m.httpRequests)
	httpDurations := cloneMap(m.httpRequestDuration)
	runtimeInvocations := cloneMap(m.runtimeInvocations)
	runtimeDurations := cloneMap(m.runtimeDuration)
	runtimeErrors := cloneMap(m.runtimeErrors)
	scrapeErrors := m.metricsScrapeErrors
	m.mu.Unlock()

	for _, key := range sortedHTTPKeys(httpRequests) {
		labels := prometheusLabels{
			"surface": key.surface,
			"method":  key.method,
			"status":  strconv.Itoa(key.status),
		}
		w.metric("quack_http_requests_total", "counter", "HTTP requests handled since process boot.", labels, float64(httpRequests[key]))
		w.metric("quack_http_request_duration_seconds_sum", "counter", "Total HTTP request duration in seconds since process boot.", labels, httpDurations[key])
		w.metric("quack_http_request_duration_seconds_count", "counter", "HTTP requests with recorded duration since process boot.", labels, float64(httpRequests[key]))
	}

	for _, key := range sortedRuntimeKeys(runtimeInvocations) {
		labels := prometheusLabels{
			"site":         key.site,
			"route":        key.route,
			"runtime_kind": key.runtimeKind,
			"result":       key.result,
		}
		w.metric("quack_starlark_invocations_total", "counter", "Starlark runtime invocations since process boot.", labels, float64(runtimeInvocations[key]))
		w.metric("quack_starlark_invocation_duration_seconds_sum", "counter", "Total Starlark invocation duration in seconds since process boot.", labels, runtimeDurations[key])
		w.metric("quack_starlark_invocation_duration_seconds_count", "counter", "Starlark invocations with recorded duration since process boot.", labels, float64(runtimeInvocations[key]))
	}

	for _, key := range sortedRuntimeErrorKeys(runtimeErrors) {
		w.metric("quack_starlark_errors_total", "counter", "Starlark runtime errors since process boot.", prometheusLabels{
			"site":  key.site,
			"route": key.route,
			"kind":  key.kind,
		}, float64(runtimeErrors[key]))
	}

	var snapshot domain.MetricsSnapshot
	var err error
	if m.db != nil {
		snapshot, err = m.db.MetricsSnapshot(ctx)
	}
	if err != nil {
		m.mu.Lock()
		m.metricsScrapeErrors++
		scrapeErrors = m.metricsScrapeErrors
		m.mu.Unlock()
		w.comment("database metrics snapshot failed: " + err.Error())
	}
	w.metric("quack_metrics_scrape_errors_total", "counter", "Metrics scrape collection errors since process boot.", nil, float64(scrapeErrors))

	w.metric("quack_users_total", "gauge", "Users in the quack database.", nil, float64(snapshot.UserCount))
	w.metric("quack_sites_total", "gauge", "Sites in the quack database.", nil, float64(snapshot.SiteCount))
	w.metric("quack_sites_live_total", "gauge", "Sites currently marked live.", nil, float64(snapshot.LiveSiteCount))
	w.metric("quack_sites_unpublished_total", "gauge", "Sites currently marked unpublished.", nil, float64(snapshot.UnpublishedSiteCount))
	w.metric("quack_uploads_total", "gauge", "Uploads by state.", prometheusLabels{"state": "finished"}, float64(snapshot.FinishedUploadCount))
	w.metric("quack_uploads_total", "gauge", "Uploads by state.", prometheusLabels{"state": "uploading"}, float64(snapshot.UploadingUploadCount))
	w.metric("quack_uploads_total", "gauge", "Uploads by state.", prometheusLabels{"state": "error"}, float64(snapshot.FailedUploadCount))
	w.metric("quack_uploads_all_total", "gauge", "Uploads in all states.", nil, float64(snapshot.UploadCount))
	w.metric("quack_upload_storage_bytes", "gauge", "Total bytes across finished upload versions.", nil, float64(snapshot.UploadBytes))
	w.metric("quack_current_site_bytes", "gauge", "Total bytes across current site versions.", nil, float64(snapshot.CurrentSiteBytes))
	w.metric("quack_upload_files_total", "gauge", "Upload file rows across finished uploads.", nil, float64(snapshot.UploadFileCount))
	w.metric("quack_runtime_routes_total", "gauge", "Runtime routes in finished uploads.", nil, float64(snapshot.RuntimeRouteCount))
	w.metric("quack_current_runtime_routes_total", "gauge", "Runtime routes attached to current live site versions.", nil, float64(snapshot.CurrentRuntimeRouteCount))
	w.metric("quack_runtime_routes_by_kind", "gauge", "Runtime routes in finished uploads by route kind.", prometheusLabels{"kind": "http"}, float64(snapshot.RuntimeHTTPRouteCount))
	w.metric("quack_runtime_routes_by_kind", "gauge", "Runtime routes in finished uploads by route kind.", prometheusLabels{"kind": "websocket"}, float64(snapshot.RuntimeWebSocketRouteCount))
	w.metric("quack_policy_violations_total", "gauge", "Policy violations recorded in the database.", nil, float64(snapshot.PolicyViolationCount))
	w.metric("quack_policy_violations_unresolved", "gauge", "Unresolved policy violations recorded in the database.", nil, float64(snapshot.UnresolvedPolicyViolationCount))

	for _, user := range snapshot.Users {
		labels := prometheusLabels{"user_id": strconv.FormatInt(user.ID, 10), "username": user.Username}
		w.metric("quack_user_sites", "gauge", "Sites owned by a user.", labels, float64(user.SiteCount))
		w.metric("quack_user_site_versions", "gauge", "Finished site versions owned by a user.", labels, float64(user.VersionCount))
		w.metric("quack_user_storage_bytes", "gauge", "Bytes across a user's sites and finished versions.", labels, float64(user.Bytes))
	}

	for _, site := range snapshot.Sites {
		labels := prometheusLabels{"site": site.Site, "site_sha": site.SiteSHA, "live_state": site.LiveState}
		w.metric("quack_site_versions", "gauge", "Finished versions for a site.", labels, float64(site.VersionCount))
		w.metric("quack_site_storage_bytes", "gauge", "Bytes across all finished versions for a site.", labels, float64(site.UploadBytes))
		w.metric("quack_site_current_bytes", "gauge", "Bytes in the current version for a site.", labels, float64(site.CurrentBytes))
		w.metric("quack_site_current_files", "gauge", "Files in the current version for a site.", labels, float64(site.CurrentFiles))
		w.metric("quack_site_runtime_routes", "gauge", "Runtime routes across finished versions for a site.", labels, float64(site.RuntimeRoutes))
	}

	activeWebSockets := m.runtime.activeWebSocketsTotal()
	w.metric("quack_websockets_active", "gauge", "Active WebSocket connections.", nil, float64(activeWebSockets))
	for _, site := range sortedStringInt64Map(m.runtime.activeWebSocketsBySite()) {
		w.metric("quack_websockets_active_by_site", "gauge", "Active WebSocket connections by site.", prometheusLabels{"site": site.key}, float64(site.value))
	}

	for _, site := range sortedStringInt64Map(modules.MemoryUsageBySite()) {
		w.metric("quack_starlark_memory_bytes", "gauge", "Bytes used by the Starlark memory module by site.", prometheusLabels{"site": site.key}, float64(site.value))
	}
}

type prometheusLabels map[string]string

type prometheusWriter struct {
	w     http.ResponseWriter
	types map[string]string
}

func newPrometheusWriter(w http.ResponseWriter) *prometheusWriter {
	return &prometheusWriter{w: w, types: map[string]string{}}
}

func (w *prometheusWriter) comment(text string) {
	text = strings.ReplaceAll(text, "\n", " ")
	fmt.Fprintf(w.w, "# %s\n", text)
}

func (w *prometheusWriter) metric(name string, typ string, help string, labels prometheusLabels, value float64) {
	if _, ok := w.types[name]; !ok {
		w.types[name] = typ
		fmt.Fprintf(w.w, "# HELP %s %s\n", name, escapeHelp(help))
		fmt.Fprintf(w.w, "# TYPE %s %s\n", name, typ)
	}
	fmt.Fprintf(w.w, "%s%s %s\n", name, labels.format(), prometheusFloat(value))
}

func (labels prometheusLabels) format() string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(key)
		b.WriteString(`="`)
		b.WriteString(escapeLabel(labels[key]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func escapeHelp(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return strings.ReplaceAll(s, "\n", "\\n")
}

func escapeLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return strings.ReplaceAll(s, `"`, `\"`)
}

func prometheusFloat(value float64) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.IsInf(value, 1) {
		return "+Inf"
	}
	if math.IsInf(value, -1) {
		return "-Inf"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func sortedHTTPKeys(m map[httpRequestKey]uint64) []httpRequestKey {
	keys := make([]httpRequestKey, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].surface != keys[j].surface {
			return keys[i].surface < keys[j].surface
		}
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].status < keys[j].status
	})
	return keys
}

func sortedRuntimeKeys(m map[runtimeInvocationKey]uint64) []runtimeInvocationKey {
	keys := make([]runtimeInvocationKey, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].site != keys[j].site {
			return keys[i].site < keys[j].site
		}
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		if keys[i].runtimeKind != keys[j].runtimeKind {
			return keys[i].runtimeKind < keys[j].runtimeKind
		}
		return keys[i].result < keys[j].result
	})
	return keys
}

func sortedRuntimeErrorKeys(m map[runtimeErrorKey]uint64) []runtimeErrorKey {
	keys := make([]runtimeErrorKey, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].site != keys[j].site {
			return keys[i].site < keys[j].site
		}
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		return keys[i].kind < keys[j].kind
	})
	return keys
}

type stringInt64 struct {
	key   string
	value int64
}

func sortedStringInt64Map(m map[string]int64) []stringInt64 {
	out := make([]stringInt64, 0, len(m))
	for key, value := range m {
		out = append(out, stringInt64{key: key, value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].key < out[j].key
	})
	return out
}

func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
