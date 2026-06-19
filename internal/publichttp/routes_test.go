package publichttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
	decision := h.decide(req)

	if decision != (PublicRouteDecision{Site: "foo", Kind: RouteStatic, Path: "/assets/app.css"}) {
		t.Fatalf("decision = %+v, want static route for foo", decision)
	}
}

type recordingStaticHandler struct {
	called  bool
	request statichttp.Request
}

func (h *recordingStaticHandler) ServeSiteFile(w http.ResponseWriter, r *http.Request, req statichttp.Request) {
	h.called = true
	h.request = req
	w.WriteHeader(http.StatusNoContent)
}
