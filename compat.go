package web

import (
	"net/http"
	"net/http/httptest"
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
