package publichttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"quack/internal/domain"
	"quack/internal/releases"
	appruntime "quack/internal/runtime"
	"quack/internal/runtimehttp"
	appsettings "quack/internal/settings"
	"quack/internal/statichttp"
)

func TestHandlerRoutesHostSiteToStaticHandler(t *testing.T) {
	static := &recordingStaticHandler{}
	h := New(static)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/blog/", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if static.request.Site != "foo" || static.request.URLPath != "/blog/" {
		t.Fatalf("static request = %+v, want foo /blog/", static.request)
	}
}

func TestHandlerPassesStaticRouteRootToStaticHandler(t *testing.T) {
	static := &recordingStaticHandler{}
	h := New(static, WithRoutes(staticRouteReader{decision: PublicRouteDecision{
		Site: "foo", Kind: RouteStatic, Path: "/assets/app.css", RoutePath: "/assets", StaticRoot: "public/assets",
	}}))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if static.request.Site != "foo" || static.request.URLPath != "/assets/app.css" || static.request.RoutePath != "/assets" || static.request.StaticRoot != "public/assets" {
		t.Fatalf("static request = %+v, want route-root static request", static.request)
	}
}

func TestHandlerPassesStaticRouteFileToStaticHandler(t *testing.T) {
	static := &recordingStaticHandler{}
	h := New(static, WithRoutes(staticRouteReader{decision: PublicRouteDecision{
		Site: "foo", Kind: RouteStatic, Path: "/favicon.ico", RoutePath: "/favicon.ico", StaticFile: "media/favicon.ico",
	}}))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if static.request.Site != "foo" || static.request.URLPath != "/favicon.ico" || static.request.RoutePath != "/favicon.ico" || static.request.StaticFile != "media/favicon.ico" {
		t.Fatalf("static request = %+v, want route-file static request", static.request)
	}
}

func TestHandlerRejectsUnknownPublicHostBeforeStaticServing(t *testing.T) {
	static := &recordingStaticHandler{}
	h := New(static)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "v1.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if static.called {
		t.Fatal("static handler called for reserved public host")
	}
}

func TestHandlerIntroducesStaticRouteDecision(t *testing.T) {
	h := New(&recordingStaticHandler{})

	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	req.Host = "foo.example.com"
	decision, err := h.decide(req)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(decision, PublicRouteDecision{Site: "foo", Kind: RouteStatic, Path: "/assets/app.css"}) {
		t.Fatalf("decision = %+v, want static route for foo", decision)
	}
}

func TestHandlerRoutesDeclaredHTTPRouteToDisabledRuntime(t *testing.T) {
	static := &recordingStaticHandler{}
	h := New(static, WithRoutes(staticRouteReader{decision: PublicRouteDecision{Site: "foo", Version: 4, Kind: RouteHTTP, Path: "/api"}}))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
	if static.called {
		t.Fatal("static handler called for dynamic route")
	}
}

func TestHandlerReturnsForbiddenForDeniedDynamicRoute(t *testing.T) {
	static := &recordingStaticHandler{}
	h := New(static, WithRoutes(staticRouteReader{decision: PublicRouteDecision{Site: "foo", Version: 4, Kind: RouteHTTP, Path: "/api", DeniedReason: "dynamic HTTP disabled"}}))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if static.called {
		t.Fatal("static handler called for denied dynamic route")
	}
}

func TestHandlerAllowsPostToDeclaredRuntimeRoute(t *testing.T) {
	runtime := recordingRuntimeService{}
	h := New(
		&recordingStaticHandler{},
		WithRoutes(staticRouteReader{decision: PublicRouteDecision{Site: "foo", Version: 4, Kind: RouteHTTP, Path: "/api/echo", Methods: []string{http.MethodPost}}}),
		WithRuntime(runtimehttp.New(&runtime)),
	)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/echo?x=1", strings.NewReader("hello"))
	req.Host = "foo.example.com"
	req.Header.Set("X-Test", "visible")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if runtime.request.Method != http.MethodPost || runtime.request.Route != "/api/echo" || runtime.request.Query != "x=1" || string(runtime.request.Body) != "hello" {
		t.Fatalf("runtime request = %+v body=%q, want POST /api/echo query and body", runtime.request, string(runtime.request.Body))
	}
	if runtime.request.Headers["X-Test"][0] != "visible" {
		t.Fatalf("runtime headers = %+v, want public header copied", runtime.request.Headers)
	}
}

func TestHandlerRejectsUndeclaredRuntimeMethod(t *testing.T) {
	runtime := recordingRuntimeService{}
	h := New(
		&recordingStaticHandler{},
		WithRoutes(staticRouteReader{decision: PublicRouteDecision{Site: "foo", Version: 4, Kind: RouteHTTP, Path: "/api", Methods: []string{http.MethodGet}}}),
		WithRuntime(runtimehttp.New(&runtime)),
	)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
	if runtime.called {
		t.Fatal("runtime was called for undeclared method")
	}
}

func TestHandlerStillRejectsPostToStaticRoute(t *testing.T) {
	static := &recordingStaticHandler{}
	h := New(static)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
	if static.called {
		t.Fatal("static handler called for POST")
	}
}

func TestHandlerRoutesDeclaredWebSocketRouteToDisabledRuntime(t *testing.T) {
	h := New(&recordingStaticHandler{}, WithRoutes(staticRouteReader{decision: PublicRouteDecision{Site: "foo", Version: 4, Kind: RouteWebSocket, Path: "/socket"}}))
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/socket", nil)
	req.Host = "foo.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotImplemented, rec.Body.String())
	}
}

func TestReleaseRouteReaderConvertsReleaseDecision(t *testing.T) {
	reader := ReleaseRouteReader{
		Releases: fakeReleaseRoutes{decision: releases.RouteDecision{Site: "foo", Version: 9, Kind: releases.RouteHTTP, Path: "/api"}},
		Policies: fakePolicyLoader{policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingRuntimeHTTPFeature,
			Mode:      "allow",
		}}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api", nil)

	decision, ok, err := reader.LookupRoute(req, "foo", "/api")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(decision, PublicRouteDecision{Site: "foo", Version: 9, Kind: RouteHTTP, Path: "/api"}) {
		t.Fatalf("decision = %+v ok=%v, want converted release route", decision, ok)
	}
}

func TestReleaseRouteReaderDeniesDynamicRouteWhenDisabledGlobally(t *testing.T) {
	reader := ReleaseRouteReader{
		Releases: fakeReleaseRoutes{decision: releases.RouteDecision{Site: "foo", Version: 9, Kind: releases.RouteHTTP, Path: "/api"}},
		Policies: fakePolicyLoader{},
	}
	req := httptest.NewRequest(http.MethodGet, "/api", nil)

	decision, ok, err := reader.LookupRoute(req, "foo", "/api")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.DeniedReason != "dynamic HTTP routes are disabled by administrator policy" {
		t.Fatalf("decision = %+v ok=%v, want globally disabled dynamic route", decision, ok)
	}
}

func TestReleaseRouteReaderDeniesDynamicRouteByPolicy(t *testing.T) {
	reader := ReleaseRouteReader{
		Releases: fakeReleaseRoutes{decision: releases.RouteDecision{Site: "foo", Version: 9, Kind: releases.RouteHTTP, Path: "/api"}},
		Policies: fakePolicyLoader{policies: []domain.PolicyRecord{{
			ScopeType: domain.ScopeSystem,
			Key:       appsettings.SettingRuntimeHTTPFeature,
			Mode:      "deny",
			Reason:    "runtime HTTP paused",
		}}},
	}
	req := httptest.NewRequest(http.MethodGet, "/api", nil)

	decision, ok, err := reader.LookupRoute(req, "foo", "/api")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.DeniedReason != "runtime HTTP paused" {
		t.Fatalf("decision = %+v ok=%v, want policy denial", decision, ok)
	}
}

func TestReleaseRouteReaderKeepsStaticAndUnknownRoutesUnaffected(t *testing.T) {
	reader := ReleaseRouteReader{
		Releases: fakeReleaseRoutes{decision: releases.RouteDecision{Site: "foo", Version: 9, Kind: releases.RouteStatic, Path: "/missing"}},
		Policies: fakePolicyLoader{},
	}
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)

	decision, ok, err := reader.LookupRoute(req, "foo", "/missing")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision.Kind != RouteStatic || decision.DeniedReason != "" {
		t.Fatalf("decision = %+v ok=%v, want unaffected static route", decision, ok)
	}
}

type recordingStaticHandler struct {
	called  bool
	request statichttp.Request
}

type staticRouteReader struct {
	decision PublicRouteDecision
}

func (r staticRouteReader) LookupRoute(req *http.Request, site string, path string) (PublicRouteDecision, bool, error) {
	return r.decision, true, nil
}

type fakeReleaseRoutes struct {
	releases.Service
	decision releases.RouteDecision
}

func (r fakeReleaseRoutes) LookupRoute(ctx context.Context, site string, path string) (releases.RouteDecision, bool, error) {
	return r.decision, true, nil
}

type fakePolicyLoader struct {
	policies []domain.PolicyRecord
}

func (l fakePolicyLoader) LoadPolicies(ctx context.Context, scopes []domain.PolicyScope) ([]domain.PolicyRecord, error) {
	var out []domain.PolicyRecord
	for _, p := range l.policies {
		for _, scope := range scopes {
			if p.ScopeType == scope.Type && p.ScopeID == scope.ID {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func (h *recordingStaticHandler) ServeSiteFile(w http.ResponseWriter, r *http.Request, req statichttp.Request) {
	h.called = true
	h.request = req
	w.WriteHeader(http.StatusNoContent)
}

type recordingRuntimeService struct {
	called  bool
	request appruntime.InvocationRequest
}

func (s *recordingRuntimeService) InvokeHTTP(ctx context.Context, req appruntime.InvocationRequest) (appruntime.InvocationResponse, error) {
	s.called = true
	s.request = req
	return appruntime.InvocationResponse{
		StatusCode: http.StatusCreated,
		Headers:    map[string][]string{"Content-Type": {"text/plain"}},
		Body:       []byte("runtime ok"),
	}, nil
}
