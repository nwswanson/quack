package publichttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"quack/internal/releases"
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

	if decision != (PublicRouteDecision{Site: "foo", Kind: RouteStatic, Path: "/assets/app.css"}) {
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
	reader := ReleaseRouteReader{Releases: fakeReleaseRoutes{decision: releases.RouteDecision{Site: "foo", Version: 9, Kind: releases.RouteHTTP, Path: "/api"}}}
	req := httptest.NewRequest(http.MethodGet, "/api", nil)

	decision, ok, err := reader.LookupRoute(req, "foo", "/api")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || decision != (PublicRouteDecision{Site: "foo", Version: 9, Kind: RouteHTTP, Path: "/api"}) {
		t.Fatalf("decision = %+v ok=%v, want converted release route", decision, ok)
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

func (h *recordingStaticHandler) ServeSiteFile(w http.ResponseWriter, r *http.Request, req statichttp.Request) {
	h.called = true
	h.request = req
	w.WriteHeader(http.StatusNoContent)
}
