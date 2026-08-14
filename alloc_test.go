//go:build !race

package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	web "example.com/web"
)

// nullWriter is a zero-allocation http.ResponseWriter for allocation tests.
// Pointer receivers keep the interface conversion allocation-free, and
// WriteString mirrors net/http's response writer so text responses need no
// string→[]byte copy.
//
// Note: Header().Set allocates a new []string per call — in real net/http
// writers too, so gin pays the same cost. The allocation budgets below
// include that writer-side allocation.
var nullHeader = http.Header{}

type nullWriter struct{}

func (*nullWriter) Header() http.Header { return nullHeader }
func (*nullWriter) Write(b []byte) (int, error) {
	return len(b), nil
}
func (*nullWriter) WriteString(s string) (int, error) { return len(s), nil }
func (*nullWriter) WriteHeader(int)                   {}

func newReq(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, nil)
}

type allocPayload struct {
	Message string `json:"message"`
	Num     int    `json:"num"`
}

var allocPayloadVal = &allocPayload{Message: "Hello, World!", Num: 42}

type allocIn struct {
	ID  int64
	Tag string
}

// The endpoint pipeline is build + handler + render — three direct calls,
// no reflection anywhere (see PROPOSAL.md v3 §3). Budgets below only need to
// cover writer-side and encoding costs:
//  1. Header().Set's []string allocation — paid by real net/http writers and
//     gin alike;
//  2. json.Marshal's output buffer.
// Routing, matching, param capture, input construction and rendering logic
// itself are allocation-free on these paths.

func TestTextAllocs(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/hello"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "Hello, World!", nil }))
	w := &nullWriter{}
	req := newReq("/hello")
	allocs := testing.AllocsPerRun(100, func() { app.ServeHTTP(w, req) })
	if allocs > 1 {
		t.Fatalf("text route: got %v allocs/req, want <= 1", allocs)
	}
}

func TestParamTextAllocs(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/user/{id}"), web.PathInt64("id"), web.Text(),
		func(id int64) (string, error) { return "ok", nil }))
	w := &nullWriter{}
	req := newReq("/user/42")
	allocs := testing.AllocsPerRun(100, func() { app.ServeHTTP(w, req) })
	// Param capture (pooled slice), matching and strconv parsing add nothing.
	if allocs > 1 {
		t.Fatalf("param text route: got %v allocs/req, want <= 1", allocs)
	}
}

func TestJSONAllocs(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/json"), web.NoIn(), web.JSON[*allocPayload](),
		func(web.None) (*allocPayload, error) { return allocPayloadVal, nil }))
	w := &nullWriter{}
	req := newReq("/json")
	allocs := testing.AllocsPerRun(100, func() { app.ServeHTTP(w, req) })
	if allocs > 2 {
		t.Fatalf("json route: got %v allocs/req, want <= 2", allocs)
	}
}

func TestInFuncStructAllocs(t *testing.T) {
	app := web.New()
	desc := web.InFunc(func(r web.Req) (allocIn, error) {
		id, err := r.Path().Int64("id")
		if err != nil {
			return allocIn{}, err
		}
		tag, err := r.Path().String("tag")
		if err != nil {
			return allocIn{}, err
		}
		return allocIn{ID: id, Tag: tag}, nil
	})
	app.Must(web.Handle(web.Get("/user/{id}/{tag}"), desc, web.Text(),
		func(in allocIn) (string, error) { return "ok", nil }))
	w := &nullWriter{}
	req := newReq("/user/42/x")
	allocs := testing.AllocsPerRun(100, func() { app.ServeHTTP(w, req) })
	// One request-object allocation for the input struct.
	if allocs > 3 {
		t.Fatalf("InFunc struct route: got %v allocs/req, want <= 3", allocs)
	}
}

func TestCtxPoolReusesContexts(t *testing.T) {
	app := web.New()
	var seen []*web.Ctx
	app.Use(func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			seen = append(seen, c)
			return next(c)
		}
	})
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "ok", nil }))
	w := &nullWriter{}
	req := newReq("/x")
	for i := 0; i < 10; i++ {
		app.ServeHTTP(w, req)
	}
	counts := make(map[*web.Ctx]int)
	for _, c := range seen {
		counts[c]++
	}
	reused := false
	for _, n := range counts {
		if n > 1 {
			reused = true
		}
	}
	if len(seen) != 10 || !reused {
		t.Fatalf("ctx pool did not reuse instances: seen=%d reuse=%v", len(seen), reused)
	}
}
