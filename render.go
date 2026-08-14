package web

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
)

// Renderer describes how an output value O becomes an HTTP response: status,
// content type and body. It is the OUTPUT half of an endpoint's contract —
// declared explicitly, compiled at registration, executed with plain calls.
type Renderer[O any] interface {
	ContentType() string
	StatusCode() int
	WriteBody(w io.Writer, v O) error
}

type headerSetter interface {
	SetHeader(h http.Header)
}

// render writes o through rd. All calls are direct; o is never boxed outside
// the renderer itself.
func render[O any](c *Ctx, o O, rd Renderer[O]) error {
	if hs, ok := any(rd).(headerSetter); ok {
		hs.SetHeader(c.Header())
	}
	if ct := rd.ContentType(); ct != "" {
		c.Header().Set("Content-Type", ct)
	}
	c.WriteHeader(rd.StatusCode())
	return rd.WriteBody(c.W, o)
}

// JSON renders T as JSON with status 200.
func JSON[T any]() Renderer[T] { return jsonRenderer[T]{} }

type jsonRenderer[T any] struct{}

func (jsonRenderer[T]) ContentType() string { return "application/json; charset=utf-8" }
func (jsonRenderer[T]) StatusCode() int     { return http.StatusOK }
func (jsonRenderer[T]) ResponseSchema() *Schema {
	return schemaOfType(reflect.TypeOf((*T)(nil)).Elem())
}
func (jsonRenderer[T]) WriteBody(w io.Writer, v T) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// Text renders a string as text/plain with status 200.
func Text() Renderer[string] { return textRenderer{} }

type textRenderer struct{}

func (textRenderer) ContentType() string     { return "text/plain; charset=utf-8" }
func (textRenderer) StatusCode() int         { return http.StatusOK }
func (textRenderer) ResponseSchema() *Schema { return &Schema{Type: "string"} }
func (textRenderer) WriteBody(w io.Writer, v string) error {
	_, err := io.WriteString(w, v)
	return err
}

// Status wraps a renderer with an explicit status code.
func Status[O any](code int, r Renderer[O]) Renderer[O] {
	return statusRenderer[O]{code: code, inner: r}
}

type statusRenderer[O any] struct {
	code  int
	inner Renderer[O]
}

func (r statusRenderer[O]) ContentType() string { return r.inner.ContentType() }
func (r statusRenderer[O]) StatusCode() int     { return r.code }
func (r statusRenderer[O]) WriteBody(w io.Writer, v O) error {
	return r.inner.WriteBody(w, v)
}

// NoContent answers with an empty 204 regardless of the value.
func NoContent[O any]() Renderer[O] { return noContentRenderer[O]{} }

type noContentRenderer[O any] struct{}

func (noContentRenderer[O]) ContentType() string          { return "" }
func (noContentRenderer[O]) StatusCode() int              { return http.StatusNoContent }
func (noContentRenderer[O]) WriteBody(io.Writer, O) error { return nil }

// Redirect answers with a Location header, an empty body and the given
// status code (301, 302, 303, 307, 308).
func Redirect[O any](code int, url string) Renderer[O] {
	return redirectRenderer[O]{code: code, url: url}
}

type redirectRenderer[O any] struct {
	code int
	url  string
}

func (r redirectRenderer[O]) ContentType() string          { return "" }
func (r redirectRenderer[O]) StatusCode() int              { return r.code }
func (r redirectRenderer[O]) WriteBody(io.Writer, O) error { return nil }
func (r redirectRenderer[O]) SetHeader(h http.Header) {
	h.Set("Location", r.url)
}

// Stream renders O = func(io.Writer) error by invoking it against the
// response writer: streaming responses (SSE, chunked, long-poll) as an
// explicit output contract. The writer implements http.Flusher on real
// servers — assert it for event streams. The handler decides what to write
// and when to stop; errors abort the response.
func Stream(ct string) Renderer[func(io.Writer) error] { return streamRenderer{ct: ct} }

type streamRenderer struct{ ct string }

func (s streamRenderer) ContentType() string { return s.ct }
func (s streamRenderer) StatusCode() int     { return http.StatusOK }
func (s streamRenderer) WriteBody(w io.Writer, v func(io.Writer) error) error {
	return v(w)
}

// Docs attaches extra OpenAPI responses to a renderer (e.g. declaring the
// 404 semantics of a GET endpoint). Runtime behavior is unchanged.
func Docs[O any](r Renderer[O], extras map[string]*Response) Renderer[O] {
	return docRenderer[O]{inner: r, extras: extras}
}

type docRenderer[O any] struct {
	inner  Renderer[O]
	extras map[string]*Response
}

func (d docRenderer[O]) ContentType() string                  { return d.inner.ContentType() }
func (d docRenderer[O]) StatusCode() int                      { return d.inner.StatusCode() }
func (d docRenderer[O]) WriteBody(w io.Writer, v O) error     { return d.inner.WriteBody(w, v) }
func (d docRenderer[O]) ExtraResponses() map[string]*Response { return d.extras }

// Error response writers shared with middleware and the app.

func writeJSONError(c *Ctx, code int, msg string) {
	b, _ := json.Marshal(map[string]string{"error": msg})
	c.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.WriteHeader(code)
	_, _ = c.W.Write(b)
}

// writeJSONErrorStatic writes a precomputed body: the hot 404/405 paths must
// not allocate a fresh JSON document per request.
func writeJSONErrorStatic(c *Ctx, code int, body []byte) {
	c.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.WriteHeader(code)
	_, _ = c.W.Write(body)
}

var (
	errNotFoundBody         = []byte(`{"error":"not found"}`)
	errMethodNotAllowedBody = []byte(`{"error":"method not allowed"}`)
)
