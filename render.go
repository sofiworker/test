package web

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
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

// preparer is implemented by built-in serializing renderers. Prepare runs
// BEFORE any response header is committed — a serialization failure flows
// through the error pipeline (500) instead of producing a 200 with an empty
// body — and it is STATELESS: it returns the bytes instead of caching them,
// because renderer instances are shared across concurrent requests.
// Streaming renderers (SSE, Stream) cannot prepare; their failures are
// inherently mid-stream.
type preparer interface {
	Prepare(v any) ([]byte, error)
}

// preparedBody writes bytes already serialized by Prepare.
type preparedBody interface {
	WritePrepared(w io.Writer, b []byte) error
}

// render writes o through rd. All calls are direct; o is never boxed outside
// the renderer itself.
func render[O any](c *Ctx, o O, rd Renderer[O]) error {
	if hs, ok := any(rd).(headerSetter); ok {
		hs.SetHeader(c.Header())
	}
	if ct := rd.ContentType(); ct != "" {
		setCT(c.Header(), ct)
	}
	var prepared []byte
	if p, ok := any(rd).(preparer); ok {
		b, err := p.Prepare(o)
		if err != nil {
			return err
		}
		prepared = b
	}
	c.WriteHeader(rd.StatusCode())
	if prepared != nil {
		if pw, ok := any(rd).(preparedBody); ok {
			return pw.WritePrepared(c.W, prepared)
		}
	}
	return rd.WriteBody(c.W, o)
}

// JSON renders T as JSON with status 200.
func JSON[T any]() Renderer[T] { return &jsonRenderer[T]{} }

type jsonRenderer[T any] struct{}

func (jsonRenderer[T]) ContentType() string { return "application/json; charset=utf-8" }
func (jsonRenderer[T]) StatusCode() int     { return http.StatusOK }
func (jsonRenderer[T]) ResponseSchema() *Schema {
	return schemaOfType(reflect.TypeOf((*T)(nil)).Elem())
}
func (jsonRenderer[T]) Prepare(v any) ([]byte, error) { return jsonMarshal(v) }
func (jsonRenderer[T]) WritePrepared(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}
func (jsonRenderer[T]) WriteBody(w io.Writer, v T) error {
	b, err := jsonMarshal(v)
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
func (r statusRenderer[O]) Prepare(v any) ([]byte, error) {
	if p, ok := any(r.inner).(preparer); ok {
		return p.Prepare(v)
	}
	return nil, nil
}
func (r statusRenderer[O]) WritePrepared(w io.Writer, b []byte) error {
	if pw, ok := any(r.inner).(preparedBody); ok {
		return pw.WritePrepared(w, b)
	}
	return nil
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

// WithHeader attaches a response header to an output contract: it is set at
// render time and declared in the OpenAPI document. Chain for more headers.
func WithHeader[O any](r Renderer[O], name, value string) Renderer[O] {
	return headerRenderer[O]{inner: r, name: name, value: value}
}

type headerRenderer[O any] struct {
	inner       Renderer[O]
	name, value string
}

func (h headerRenderer[O]) ContentType() string { return h.inner.ContentType() }
func (h headerRenderer[O]) StatusCode() int     { return h.inner.StatusCode() }
func (h headerRenderer[O]) WriteBody(w io.Writer, v O) error {
	return h.inner.WriteBody(w, v)
}
func (h headerRenderer[O]) SetHeader(hh http.Header) {
	if s, ok := any(h.inner).(headerSetter); ok {
		s.SetHeader(hh)
	}
	hh.Set(h.name, h.value)
}
func (h headerRenderer[O]) ResponseHeaders() map[string]string {
	m := map[string]string{}
	if in, ok := any(h.inner).(interface{ ResponseHeaders() map[string]string }); ok {
		for k, v := range in.ResponseHeaders() {
			m[k] = v
		}
	}
	m[h.name] = h.value
	return m
}

// SSE renders O = func(*SSEWriter) error as a text/event-stream: a typed
// event writer for server-sent events.
func SSE() Renderer[func(*SSEWriter) error] { return sseRenderer{} }

type sseRenderer struct{}

func (sseRenderer) ContentType() string { return "text/event-stream" }
func (sseRenderer) StatusCode() int     { return http.StatusOK }
func (sseRenderer) WriteBody(w io.Writer, v func(*SSEWriter) error) error {
	var f http.Flusher
	if fl, ok := w.(http.Flusher); ok {
		f = fl
	}
	return v(&SSEWriter{w: w, f: f})
}

// SSEWriter writes server-sent events and flushes when the writer supports
// it.
type SSEWriter struct {
	w io.Writer
	f http.Flusher
}

func (s *SSEWriter) flush() {
	if s.f != nil {
		s.f.Flush()
	}
}

// Event starts a named event; chain Data/Text/Retry on the returned value.
func (s *SSEWriter) Event(name string) *SSEEvent { return &SSEEvent{w: s, name: name} }

// Comment sends a comment line (": text").
func (s *SSEWriter) Comment(text string) error {
	if _, err := fmt.Fprintf(s.w, ": %s\n", text); err != nil {
		return err
	}
	s.flush()
	return nil
}

// Ping sends a keep-alive comment.
func (s *SSEWriter) Ping() error {
	if _, err := io.WriteString(s.w, ": ping\n\n"); err != nil {
		return err
	}
	s.flush()
	return nil
}

// SSEEvent is one server-sent event under construction.
type SSEEvent struct {
	w     *SSEWriter
	name  string
	id    string
	retry int
}

// ID sets the event id (emitted as an id: field).
func (e *SSEEvent) ID(id string) *SSEEvent {
	e.id = id
	return e
}

// Retry sets the client reconnection delay in milliseconds (emitted as a
// retry: field).
func (e *SSEEvent) Retry(ms int) *SSEEvent {
	e.retry = ms
	return e
}

// Data sends v as JSON in a data: line and flushes.
func (e *SSEEvent) Data(v any) error {
	b, err := jsonMarshal(v)
	if err != nil {
		return err
	}
	return e.send(string(b))
}

// Text sends s verbatim in a data: line and flushes.
func (e *SSEEvent) Text(s string) error { return e.send(s) }

func (e *SSEEvent) send(data string) error {
	var buf strings.Builder
	if e.name != "" {
		fmt.Fprintf(&buf, "event: %s\n", e.name)
	}
	if e.id != "" {
		fmt.Fprintf(&buf, "id: %s\n", e.id)
	}
	if e.retry > 0 {
		fmt.Fprintf(&buf, "retry: %d\n", e.retry)
	}
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(&buf, "data: %s\n", line)
	}
	buf.WriteString("\n")
	if _, err := io.WriteString(e.w.w, buf.String()); err != nil {
		return err
	}
	e.w.flush()
	return nil
}

// setCT 写入每响应独立的内容类型切片：共享切片会让中间件对 [0] 的
// 原地修改污染所有后续请求（真实数据竞争，已被外部测试复现）。
// 直接写 map 仍跳过 Set 的键校验与规范化，只付一次小分配。
func setCT(h http.Header, v string) {
	h["Content-Type"] = []string{v}
}

// writeJSON is the direct JSON path: codec marshal + frozen header + write.
func writeJSON(c *Ctx, v any) error {
	b, err := jsonMarshal(v)
	if err != nil {
		return err
	}
	setCT(c.Header(), "application/json; charset=utf-8")
	c.WriteHeader(c.status)
	_, err = c.W.Write(b)
	return err
}

// writeText is the direct text path: no renderer interface, no Set.
func writeText(c *Ctx, s string) error {
	setCT(c.Header(), "text/plain; charset=utf-8")
	c.WriteHeader(http.StatusOK)
	_, err := io.WriteString(c.W, s)
	return err
}

// writeProblemError renders RFC 7807 problem+json with the typed error's
// structured fields as extra members.
func writeProblemError(c *Ctx, code int, detail string, fields map[string]any) {
	m := map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(code),
		"status": code,
	}
	if detail != "" {
		m["detail"] = detail
	}
	m["instance"] = c.Path()
	for k, v := range fields {
		m[k] = v
	}
	b, _ := jsonMarshal(m)
	setCT(c.Header(), "application/json; charset=utf-8")
	c.WriteHeader(code)
	_, _ = c.W.Write(b)
}

// SetCookie attaches a Set-Cookie header to an output contract: set at
// render time, chainable, and visible in the contract itself.
func SetCookie[O any](r Renderer[O], ck *http.Cookie) Renderer[O] {
	return cookieRenderer[O]{inner: r, cookie: ck}
}

type cookieRenderer[O any] struct {
	inner  Renderer[O]
	cookie *http.Cookie
}

func (c cookieRenderer[O]) ContentType() string { return c.inner.ContentType() }
func (c cookieRenderer[O]) StatusCode() int     { return c.inner.StatusCode() }
func (c cookieRenderer[O]) WriteBody(w io.Writer, v O) error {
	return c.inner.WriteBody(w, v)
}
func (c cookieRenderer[O]) SetHeader(h http.Header) {
	if s, ok := any(c.inner).(headerSetter); ok {
		s.SetHeader(h)
	}
	h.Add("Set-Cookie", c.cookie.String())
}

// Download renders bytes as an attachment download with the given filename.
func Download(filename string) Renderer[[]byte] { return downloadRenderer{name: filename} }

type downloadRenderer struct{ name string }

func (d downloadRenderer) ContentType() string { return "application/octet-stream" }
func (d downloadRenderer) StatusCode() int     { return http.StatusOK }
func (d downloadRenderer) SetHeader(h http.Header) {
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, d.name))
}
func (d downloadRenderer) WriteBody(w io.Writer, v []byte) error {
	_, err := w.Write(v)
	return err
}

// StreamFile streams an io.ReadSeeker (e.g. *os.File) with the given content
// type, seeking to the start first.
func StreamFile(ct string) Renderer[io.ReadSeeker] { return streamFileRenderer{ct: ct} }

type streamFileRenderer struct{ ct string }

func (s streamFileRenderer) ContentType() string { return s.ct }
func (s streamFileRenderer) StatusCode() int     { return http.StatusOK }
func (s streamFileRenderer) WriteBody(w io.Writer, v io.ReadSeeker) error {
	if _, err := v.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := io.Copy(w, v)
	return err
}

// XML renders T as XML with status 200.
func XML[T any]() Renderer[T] { return &xmlRenderer[T]{} }

type xmlRenderer[T any] struct{}

func (xmlRenderer[T]) ContentType() string           { return "application/xml; charset=utf-8" }
func (xmlRenderer[T]) StatusCode() int               { return http.StatusOK }
func (xmlRenderer[T]) Prepare(v any) ([]byte, error) { return xml.Marshal(v) }
func (xmlRenderer[T]) WritePrepared(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}
func (xmlRenderer[T]) WriteBody(w io.Writer, v T) error {
	b, err := xml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// HTML renders v through a text/template with the given name. The template
// is user-owned: parsed once at registration, executed per request.
func HTML[T any](t *template.Template, name string) Renderer[T] {
	return &htmlRenderer[T]{t: t, name: name}
}

type htmlRenderer[T any] struct {
	t    *template.Template
	name string
}

func (h htmlRenderer[T]) ContentType() string { return "text/html; charset=utf-8" }
func (h htmlRenderer[T]) StatusCode() int     { return http.StatusOK }
func (h htmlRenderer[T]) Prepare(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := h.t.ExecuteTemplate(&buf, h.name, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func (h htmlRenderer[T]) WritePrepared(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}
func (h htmlRenderer[T]) WriteBody(w io.Writer, v T) error {
	return h.t.ExecuteTemplate(w, h.name, v)
}

// Bytes renders raw bytes with the given content type.
func Bytes(ct string) Renderer[[]byte] { return bytesRenderer{ct: ct} }

type bytesRenderer struct{ ct string }

func (b bytesRenderer) ContentType() string { return b.ct }
func (b bytesRenderer) StatusCode() int     { return http.StatusOK }
func (b bytesRenderer) WriteBody(w io.Writer, v []byte) error {
	_, err := w.Write(v)
	return err
}

// Error response writers shared with middleware and the app.

// errBodyCache reuses marshaled error bodies: the same (code, msg) pair
// renders identically, so the hot error paths (404/401/409/500) allocate
// nothing after the first occurrence.
type errBodyKey struct {
	code int
	msg  string
}

var errBodyCache sync.Map

func writeJSONError(c *Ctx, code int, msg string) {
	key := errBodyKey{code: code, msg: msg}
	var b []byte
	if v, ok := errBodyCache.Load(key); ok {
		b = v.([]byte)
	} else {
		b, _ = jsonMarshal(map[string]string{"error": msg})
		errBodyCache.Store(key, b)
	}
	setCT(c.Header(), "application/json; charset=utf-8")
	c.WriteHeader(code)
	_, _ = c.W.Write(b)
}

// writeJSONErrorStatic writes a precomputed body: the hot 404/405 paths must
// not allocate a fresh JSON document per request.
func writeJSONErrorStatic(c *Ctx, code int, body []byte) {
	setCT(c.Header(), "application/json; charset=utf-8")
	c.WriteHeader(code)
	_, _ = c.W.Write(body)
}

var (
	errNotFoundBody         = []byte(`{"error":"not found"}`)
	errMethodNotAllowedBody = []byte(`{"error":"method not allowed"}`)
)
