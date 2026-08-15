// Package web implements an endpoint-as-data HTTP framework: routes are
// first-class typed values, handlers are plain functions over declared
// input/output contracts, and there is no reflection anywhere (see
// PROPOSAL.md v3 for the design rationale).
package web

import (
	"context"
	"net/http"
	"net/url"
)

// param is a single route parameter captured during matching.
type param struct {
	key   string
	value string
}

// Ctx is the per-request context. It is pooled: handlers MUST NOT retain a
// reference to it (or any value derived from it) after they return. The race
// detector will flag violations.
type Ctx struct {
	W   http.ResponseWriter
	Req *http.Request

	status      int
	wroteHeader bool

	params []param
	query  url.Values // 惰性解析缓存：一次请求只解析一次查询串
	keys   map[any]any
}

// Method returns the request method.
func (c *Ctx) Method() string { return c.Req.Method }

// Path returns the (decoded) request path.
func (c *Ctx) Path() string { return c.Req.URL.Path }

// Context returns the request context carrying cancellation signals.
func (c *Ctx) Context() context.Context { return c.Req.Context() }

// Header returns the response header map.
func (c *Ctx) Header() http.Header { return c.W.Header() }

// Status returns the response status code (200 until a header is written).
func (c *Ctx) Status() int { return c.status }

// WriteHeader writes the status code. Only the first call has an effect.
func (c *Ctx) WriteHeader(code int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	c.status = code
	c.W.WriteHeader(code)
}

// Write writes b to the response body, materializing the status code first.
func (c *Ctx) Write(b []byte) (int, error) {
	c.WriteHeader(c.status)
	return c.W.Write(b)
}

// parsedQuery returns the request's parsed query string, cached per request.
func (c *Ctx) parsedQuery() url.Values {
	if c.query == nil {
		c.query = c.Req.URL.Query()
	}
	return c.query
}

// Param returns the value of a route parameter, or "" if absent.
// Note: an absent parameter and an empty parameter value are indistinguishable
// here; the typed accessors (Req.Path().*) treat both as an error.
func (c *Ctx) Param(name string) string {
	for i := range c.params {
		if c.params[i].key == name {
			return c.params[i].value
		}
	}
	return ""
}

// Key is a typed request-scoped key. Unlike gin's c.Set/c.Get, the value type
// is fixed at compile time and the key itself carries the operation:
//
//	var requestID = web.NewKey[string]("request-id")
//	requestID.Set(c, "abc123")
//	id, ok := requestID.Get(c)
type Key[T any] struct {
	name string
}

// NewKey creates a typed key. Keys should be package-level variables so that
// the same T is used by every setter and getter.
func NewKey[T any](name string) Key[T] {
	return Key[T]{name: name}
}

// Set stores v under the key for the lifetime of this request.
func (k Key[T]) Set(c *Ctx, v T) {
	if c.keys == nil {
		c.keys = make(map[any]any, 4)
	}
	c.keys[k] = v
}

// Get returns the value stored under the key and whether it was present.
func (k Key[T]) Get(c *Ctx) (T, bool) {
	v, ok := c.keys[k]
	if !ok {
		var zero T
		return zero, false
	}
	return v.(T), true
}

func (c *Ctx) reset(w http.ResponseWriter, r *http.Request, params []param) {
	c.W = w
	c.Req = r
	c.status = http.StatusOK
	c.wroteHeader = false
	c.params = params
	c.query = nil
	if len(c.keys) > 0 {
		clear(c.keys)
	}
}
