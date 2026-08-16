package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"

	"example.com/web/httperr"
)

// Middleware wraps a Handler. It is an explicit higher-order function:
// wrapping order (Use order) is the onion order, short-circuiting is simply
// not calling next, and errors flow through return values. There is no
// c.Next() and no c.Abort().
type Middleware func(Handler) Handler

func chain(mws []Middleware, h Handler) Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// App is a router plus middleware stack. Routes must be mounted before the
// App is served; mounting is not synchronized.
type App struct {
	tree *node
	mw   []Middleware

	routes []*Route // mounted routes, in order (OpenAPI generation)
	cors   *CORSConfig

	problemJSON bool // RFC 7807 error envelope when enabled

	sortOnce sync.Once // 首次请求时对路由树做一次性权重排序

	ctxPool sync.Pool
}

// New creates an App.
func New() *App {
	a := &App{tree: &node{}}
	a.ctxPool.New = func() any { return &Ctx{params: make([]param, 0, 8)} }
	return a
}

// Use appends middleware to the global stack. Middleware applies to routes
// mounted after the call.
func (a *App) Use(mws ...Middleware) {
	a.mw = append(a.mw, mws...)
}

// UseProblemJSON switches error responses to RFC 7807 problem+json:
// {"type","title","status","detail", ...typed fields}. Off by default (the
// plain {"error": msg} envelope stays the zero-config default).
func (a *App) UseProblemJSON() { a.problemJSON = true }

// UseCORS enables CORS end to end: it injects the stamping middleware and
// answers preflight requests at the App level, before routing. Preflight
// must live here — the router's automatic OPTIONS handling runs before any
// middleware chain, and auth middleware must never block preflight.
func (a *App) UseCORS(cfg CORSConfig) {
	a.cors = &cfg
	a.Use(CORS(cfg))
}

// Mount mounts a compiled Route, returning a descriptive error for invalid
// paths or conflicts.
func (a *App) Mount(r *Route) error {
	if r == nil {
		return errors.New("web: cannot mount a nil route")
	}
	method := strings.ToUpper(r.method)
	segs, err := parsePath(r.path)
	if err != nil {
		return err
	}
	all := make([]Middleware, 0, len(a.mw)+len(r.mws))
	all = append(all, a.mw...)
	all = append(all, r.mws...)
	if err := a.tree.insert(segs, chain(all, r.h), method); err != nil {
		return err
	}
	a.routes = append(a.routes, r)
	return nil
}

// Must mounts one or more Routes and panics on the first registration
// error (programmer errors).
func (a *App) Must(routes ...*Route) {
	for _, r := range routes {
		must(a.Mount(r))
	}
}

// Group bundles routes under a path prefix with group-level middleware.
// The group's middleware runs inside the app's global stack, before any
// route-level middleware.
type Group struct {
	app    *App
	prefix string
	mw     []Middleware
}

// Group creates a Group. The prefix must start with '/'.
func (a *App) Group(prefix string, mws ...Middleware) *Group {
	if prefix != "" && !strings.HasPrefix(prefix, "/") {
		panic("web: group prefix must start with '/'")
	}
	return &Group{app: a, prefix: prefix, mw: append([]Middleware(nil), mws...)}
}

// Mount mounts a Route under the group prefix with the group middleware.
func (g *Group) Mount(r *Route) error {
	if r == nil {
		return errors.New("web: cannot mount a nil route")
	}
	n := *r
	n.path = g.prefix + r.path
	n.mws = append(append([]Middleware(nil), g.mw...), r.mws...)
	return g.app.Mount(&n)
}

// Must mounts one or more Routes under the group, panicking on registration
// errors.
func (g *Group) Must(routes ...*Route) {
	for _, r := range routes {
		must(g.Mount(r))
	}
}

// Group nests a group under this one: prefixes and middleware compose.
func (g *Group) Group(prefix string, mws ...Middleware) *Group {
	if prefix != "" && !strings.HasPrefix(prefix, "/") {
		panic("web: group prefix must start with '/'")
	}
	return &Group{
		app:    g.app,
		prefix: g.prefix + prefix,
		mw:     append(append([]Middleware(nil), g.mw...), mws...),
	}
}

func must(err error) {
	if err != nil {
		panic("web: " + err.Error())
	}
}

// Handler returns the App as a plain http.Handler.
func (a *App) Handler() http.Handler { return a }

// ServeHTTP implements http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 权重排序惰性化：注册期 O(1)，首个请求一次性完成（O(N log N)）。
	a.sortOnce.Do(func() { a.tree.sortByWeight() })
	c := a.ctxPool.Get().(*Ctx)
	ps := c.params[:0]
	c.reset(w, r, ps)

	p := r.URL.Path
	pos := 0
	if len(p) > 0 && p[0] == '/' {
		pos = 1
	}
	n, nps, ok := a.tree.match(p, pos, ps)
	c.params = nps

	var err error
	switch {
	case !ok || len(n.leafHandlers) == 0:
		a.notFound(c)
	case a.cors != nil && r.Method == http.MethodOptions &&
		r.Header.Get("Access-Control-Request-Method") != "" && a.corsPreflight(c, n):
		// preflight answered; nothing else to do
		err = nil
	default:
		h := n.handlerFor(r.Method)
		if h == nil {
			if r.Method == http.MethodOptions {
				a.autoOptions(c, n)
			} else {
				a.methodNotAllowed(c, n)
			}
		} else {
			err = h(c)
		}
	}

	if err != nil && !c.hijacked {
		a.writeError(c, err)
	}
	if !c.wroteHeader && !c.hijacked {
		c.WriteHeader(c.status)
	}

	// Release the pooled Ctx. Cap the retained parameter capacity so a
	// single deep wildcard route cannot pin an ever-growing buffer.
	if cap(c.params) > 64 {
		c.params = make([]param, 0, 8)
	}
	a.ctxPool.Put(c)
}

func (a *App) notFound(c *Ctx) {
	writeJSONErrorStatic(c, http.StatusNotFound, errNotFoundBody)
}

func (a *App) methodNotAllowed(c *Ctx, n *node) {
	c.Header().Set("Allow", allowHeader(n.methods))
	writeJSONErrorStatic(c, http.StatusMethodNotAllowed, errMethodNotAllowedBody)
}

func (a *App) autoOptions(c *Ctx, n *node) {
	c.Header().Set("Allow", allowHeader(n.methods))
	c.WriteHeader(http.StatusNoContent)
}

// corsPreflight answers a CORS preflight for an allowed origin: 204 with the
// full header set. Returns false when the origin is not allowed, falling
// back to the ordinary OPTIONS handling.
func (a *App) corsPreflight(c *Ctx, n *node) bool {
	cfg := a.cors
	origin := c.Req.Header.Get("Origin")
	if allowOrigin(*cfg, origin) == "" {
		return false
	}
	ao := allowOrigin(*cfg, origin)
	c.Header().Set("Access-Control-Allow-Origin", ao)
	if cfg.AllowCredentials {
		c.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	c.Header().Set("Vary", "Origin")
	c.Header().Set("Allow", allowHeader(n.methods))
	c.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowMethods, ", "))
	c.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowHeaders, ", "))
	if cfg.MaxAgeSeconds > 0 {
		c.Header().Set("Access-Control-Max-Age", fmt.Sprint(cfg.MaxAgeSeconds))
	}
	c.WriteHeader(http.StatusNoContent)
	return true
}

func allowHeader(methods []string) string {
	all := make([]string, 0, len(methods)+1)
	all = append(all, methods...)
	all = append(all, http.MethodOptions)
	sort.Strings(all)
	return strings.Join(all, ", ")
}

// statusCode derives the status code a request will end up with: the code
// already written by the handler if any, otherwise the code carried by a
// typed httperr error, otherwise 500. Middleware can call it to observe the
// effective status before the response is finalized.
func statusCode(c *Ctx, err error) int {
	if c.wroteHeader {
		return c.status
	}
	if err == nil {
		return c.status
	}
	var he *httperr.Error
	if errors.As(err, &he) {
		return he.StatusCode()
	}
	return http.StatusInternalServerError
}

func (a *App) writeError(c *Ctx, err error) {
	if c.wroteHeader {
		return // headers already sent; nothing sensible left to do
	}
	code, msg := http.StatusInternalServerError, "internal server error"
	var he *httperr.Error
	var fields map[string]any
	if errors.As(err, &he) {
		code, msg = he.StatusCode(), he.Message()
		fields = he.Fields()
	} else {
		log.Printf("web: handler error: %v", err)
	}
	if a.problemJSON {
		writeProblemError(c, code, msg, fields)
		return
	}
	writeJSONError(c, code, msg)
}
