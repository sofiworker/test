package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
)

// FromStd adapts a stdlib http.Handler to the internal Handler. This is the
// one-line adapter promised for the net/http middleware ecosystem: any
// existing stdlib-style middleware or handler plugs in directly.
func FromStd(h http.Handler) Handler {
	return func(c *Ctx) error {
		h.ServeHTTP(c.W, c.Req)
		return nil
	}
}

// FromStdFunc adapts a stdlib handler function.
func FromStdFunc(fn func(http.ResponseWriter, *http.Request)) Handler {
	return FromStd(http.HandlerFunc(fn))
}

// Static serves files from dir under prefix (GET, HEAD via fallback).
// Example: Static("/assets/", "./public") serves ./public/css/app.css at
// /assets/css/app.css.
func Static(prefix, dir string) *Route {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	fs := http.StripPrefix(prefix, http.FileServer(http.Dir(dir)))
	return Raw("GET", prefix+"{file...}", FromStd(fs))
}

// Healthcheck returns the standard /healthz route.
func Healthcheck() *Route {
	return Handle(Get("/healthz"), NoIn(), JSON[map[string]string](),
		func(None) (map[string]string, error) {
			return map[string]string{"status": "ok"}, nil
		})
}

// SPA serves a single-page application from dir under prefix: existing files
// are served directly, anything else (including unknown routes) falls back
// to index.html. Typical use: SPA("/", "./dist").
func SPA(prefix, dir string) []*Route {
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, prefix)
		full := filepath.Join(dir, rel)
		if info, err := os.Stat(full); err != nil || info.IsDir() {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		http.ServeFile(w, r, full)
	})
	routes := []*Route{
		Raw("GET", prefix+"{path...}", FromStd(handler)),
	}
	// 前缀本身（无尾斜杠）也要命中；prefix="/" 时即根路径。
	if bare := strings.TrimSuffix(prefix, "/"); bare != "" {
		routes = append(routes, Raw("GET", bare, FromStd(handler)))
	} else {
		routes = append(routes, Raw("GET", "/", FromStd(handler)))
	}
	return routes
}

// ServeRoute mounts the route in a fresh App and serves one request: a
// one-line way to exercise an endpoint in tests and ad-hoc tooling. Routes
// are data, so a single route needs no surrounding application.
func ServeRoute(r *Route, req *http.Request) *httptest.ResponseRecorder {
	app := New()
	app.Must(r)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	return rec
}
