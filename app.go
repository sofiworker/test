package web

import (
	"errors"
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

	ctxPool   sync.Pool
	paramPool sync.Pool
}

// New creates an App.
func New() *App {
	a := &App{tree: &node{}}
	a.ctxPool.New = func() any { return &Ctx{} }
	// The pool holds *[]param so that Put never boxes a slice header.
	a.paramPool.New = func() any { s := make([]param, 0, 8); return &s }
	return a
}

// Use appends middleware to the global stack. Middleware applies to routes
// mounted after the call.
func (a *App) Use(mws ...Middleware) {
	a.mw = append(a.mw, mws...)
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
	return a.tree.insert(segs, chain(all, r.h), method)
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

func must(err error) {
	if err != nil {
		panic("web: " + err.Error())
	}
}

// Handler returns the App as a plain http.Handler.
func (a *App) Handler() http.Handler { return a }

// ServeHTTP implements http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := a.ctxPool.Get().(*Ctx)
	pp := a.paramPool.Get().(*[]param)
	ps := (*pp)[:0]
	c.reset(w, r, ps)

	p := r.URL.Path
	pos := 0
	if len(p) > 0 && p[0] == '/' {
		pos = 1
	}
	n, nps, ok := a.tree.match("", p, pos, ps)
	c.params = nps

	var err error
	switch {
	case !ok || n.handlers == nil:
		a.notFound(c)
	default:
		h := n.handlers[r.Method]
		if h == nil && r.Method == http.MethodHead {
			h = n.handlers[http.MethodGet]
		}
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

	if err != nil {
		a.writeError(c, err)
	}
	if !c.wroteHeader {
		c.WriteHeader(c.status)
	}

	// Release pooled resources. Cap the retained parameter capacity so a
	// single deep wildcard route cannot pin an ever-growing buffer.
	if cap(c.params) <= 64 {
		*pp = c.params[:0]
		a.paramPool.Put(pp)
	}
	c.params = nil
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
	if errors.As(err, &he) {
		code, msg = he.StatusCode(), he.Message()
	} else {
		log.Printf("web: handler error: %v", err)
	}
	writeJSONError(c, code, msg)
}
