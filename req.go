package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Req is the read-only, typed accessor over the INPUT half of a request. It
// exists for custom input constructors (InFunc). Unlike gin's Context it has
// no write side, no KV store and no control flow: writing happens through
// the endpoint's Renderer and return value.
//
// Accessors are grouped by source and parse into concrete types with
// strconv: no reflection, no allocation.
type Req struct{ c *Ctx }

// Context returns the request's cancellation signal.
func (r Req) Context() context.Context { return r.c.Context() }

// Raw returns the underlying *Ctx. Escape hatch for cases the typed
// accessors do not cover — e.g. reading typed keys via the key-centric API
// (key.Get(r.Raw())). Prefer the typed accessors.
func (r Req) Raw() *Ctx { return r.c }

// Header returns the request header map.
func (r Req) Header() http.Header { return r.c.Req.Header }

// Path returns the path-parameter accessor.
func (r Req) Path() PathAccessor { return PathAccessor{c: r.c} }

// Query returns the query-string accessor.
func (r Req) Query() QueryAccessor { return QueryAccessor{c: r.c} }

// PathAccessor parses route parameters by name.
type PathAccessor struct{ c *Ctx }

// String returns a non-empty path parameter.
func (p PathAccessor) String(name string) (string, error) {
	v := p.c.Param(name)
	if v == "" {
		return "", fmt.Errorf("web: path parameter %q is missing or empty", name)
	}
	return v, nil
}

// Int64 parses a path parameter as int64.
func (p PathAccessor) Int64(name string) (int64, error) {
	v, err := p.String(name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("web: path parameter %q: %w", name, err)
	}
	return n, nil
}

// Int parses a path parameter as int.
func (p PathAccessor) Int(name string) (int, error) {
	v, err := p.String(name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("web: path parameter %q: %w", name, err)
	}
	return n, nil
}

// Bool parses a path parameter as bool.
func (p PathAccessor) Bool(name string) (bool, error) {
	v, err := p.String(name)
	if err != nil {
		return false, err
	}
	n, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("web: path parameter %q: %w", name, err)
	}
	return n, nil
}

// Float64 parses a path parameter as float64.
func (p PathAccessor) Float64(name string) (float64, error) {
	v, err := p.String(name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("web: path parameter %q: %w", name, err)
	}
	return n, nil
}

// QueryAccessor reads query-string values.
type QueryAccessor struct{ c *Ctx }

// String returns the query value ("" when absent).
func (q QueryAccessor) String(name string) string { return q.c.Req.URL.Query().Get(name) }

// Int parses a query value as int.
func (q QueryAccessor) Int(name string) (int, error) {
	v := q.String(name)
	if v == "" {
		return 0, fmt.Errorf("web: query parameter %q is missing", name)
	}
	return strconv.Atoi(v)
}

// Bool parses a query value as bool.
func (q QueryAccessor) Bool(name string) (bool, error) {
	v := q.String(name)
	if v == "" {
		return false, fmt.Errorf("web: query parameter %q is missing", name)
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("web: query parameter %q: %w", name, err)
	}
	return b, nil
}

// IntDefault parses a query value as int, falling back to def when absent
// or invalid.
func (q QueryAccessor) IntDefault(name string, def int) int {
	v, err := q.Int(name)
	if err != nil {
		return def
	}
	return v
}

// DecodeBody decodes the request body into T through the JSON codec (same
// engine as the renderers; BodyLimit caps the read).
func DecodeBody[T any](r Req) (T, error) {
	var v T
	b, err := io.ReadAll(r.c.Req.Body)
	if err != nil {
		return v, err
	}
	err = jsonUnmarshal(b, &v)
	return v, err
}
