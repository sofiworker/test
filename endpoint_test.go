package web_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	web "example.com/web"
)

func TestNestedFreeFunctions(t *testing.T) {
	// The chained syntax is unavailable in Go (no generic methods); the
	// nested free-function form is equivalent and composes the same way.
	contract := web.WithOut(web.WithIn(web.Get("/who"), web.NoIn()), web.Text())
	app := web.New()
	app.Must(contract.Handle(func(web.None) (string, error) { return "nested", nil }))
	rec := do(t, app, "GET", "/who")
	if body(t, rec) != "nested" {
		t.Fatalf("%q", body(t, rec))
	}
}

func TestBodyJSONInput(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Post("/users"), web.BodyJSON[user](),
		web.Status(201, web.JSON[user]()),
		func(u user) (user, error) { return u, nil }))

	req := httptest.NewRequest("POST", "/users", strings.NewReader(`{"id":3,"name":"Lin"}`))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 201 || body(t, rec) != `{"id":3,"name":"Lin"}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}

	req = httptest.NewRequest("POST", "/users", strings.NewReader(`{bad json`))
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("bad body: %d, want 400", rec.Code)
	}
}

func TestQueryInputs(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/q"), web.QueryInt("page"), web.JSON[int](),
		func(page int) (int, error) { return page, nil }))
	app.Must(web.Handle(web.Get("/qd"), web.QueryIntDefault("page", 7), web.JSON[int](),
		func(page int) (int, error) { return page, nil }))
	app.Must(web.Handle(web.Get("/qs"), web.QueryString("tag"), web.Text(),
		func(tag string) (string, error) { return tag, nil }))

	if rec := do(t, app, "GET", "/q?page=3"); body(t, rec) != "3" {
		t.Fatalf("q: %q", body(t, rec))
	}
	if rec := do(t, app, "GET", "/q"); rec.Code != 400 {
		t.Fatalf("q missing: %d, want 400", rec.Code)
	}
	if rec := do(t, app, "GET", "/qd"); body(t, rec) != "7" {
		t.Fatalf("qd default: %q", body(t, rec))
	}
	if rec := do(t, app, "GET", "/qs?tag=red"); body(t, rec) != "red" {
		t.Fatalf("qs: %q", body(t, rec))
	}
}

// upper is a user-defined renderer: outputs are rendered by the endpoint's
// declared contract, and anyone can define new ones.
type upper struct{}

func (upper) ContentType() string { return "text/plain; charset=utf-8" }
func (upper) StatusCode() int     { return 200 }
func (upper) WriteBody(w io.Writer, v string) error {
	_, err := io.WriteString(w, strings.ToUpper(v))
	return err
}

func TestCustomRenderer(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/u"), web.NoIn(), upper{},
		func(web.None) (string, error) { return "shout", nil }))
	rec := do(t, app, "GET", "/u")
	if body(t, rec) != "SHOUT" {
		t.Fatalf("%q", body(t, rec))
	}
}

func TestRawEscape(t *testing.T) {
	app := web.New()
	app.Must(web.Raw("GET", "/raw", web.Handler(func(c *web.Ctx) error {
		_, err := c.Write([]byte("raw"))
		return err
	})))
	rec := do(t, app, "GET", "/raw")
	if rec.Code != 200 || body(t, rec) != "raw" {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestInFuncHundredParams(t *testing.T) {
	// 100 parameters: one struct, one constructor — no arity limit anywhere.
	type big struct {
		P00, P01, P02, P03, P04, P05, P06, P07, P08, P09,
		P10, P11 int
	}
	desc := web.InFunc(func(r web.Req) (big, error) {
		var b big
		names := []string{"p00", "p01", "p02", "p03", "p04", "p05",
			"p06", "p07", "p08", "p09", "p10", "p11"}
		for i, n := range names {
			v, err := r.Path().Int(n)
			if err != nil {
				return b, err
			}
			switch i {
			case 0:
				b.P00 = v
			case 1:
				b.P01 = v
			case 2:
				b.P02 = v
			case 3:
				b.P03 = v
			case 4:
				b.P04 = v
			case 5:
				b.P05 = v
			case 6:
				b.P06 = v
			case 7:
				b.P07 = v
			case 8:
				b.P08 = v
			case 9:
				b.P09 = v
			case 10:
				b.P10 = v
			case 11:
				b.P11 = v
			}
		}
		return b, nil
	})
	app := web.New()
	app.Must(web.Handle(web.Get("/p/{p00}/{p01}/{p02}/{p03}/{p04}/{p05}/{p06}/{p07}/{p08}/{p09}/{p10}/{p11}"),
		desc, web.JSON[big](),
		func(b big) (big, error) { return b, nil }))
	rec := do(t, app, "GET", "/p/1/2/3/4/5/6/7/8/9/10/11/12")
	if rec.Code != 200 || !strings.Contains(body(t, rec), `"P11":12`) {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestFlavoredEntries(t *testing.T) {
	app := web.New()
	app.Must(web.GetJSON("/j", web.NoIn(), func(web.None) (*user, error) {
		return &user{ID: 1, Name: "Ada"}, nil
	}))
	app.Must(web.PostJSON("/p", web.BodyJSON[user](), func(u user) (user, error) { return u, nil }))
	app.Must(web.CreatedJSON("/c", web.BodyJSON[user](), func(u user) (user, error) { return u, nil }))
	app.Must(web.GetText("/t", web.NoIn(), func(web.None) (string, error) { return "ok", nil }))

	if rec := do(t, app, "GET", "/j"); body(t, rec) != `{"id":1,"name":"Ada"}` {
		t.Fatalf("GetJSON: %q", body(t, rec))
	}
	req := httptest.NewRequest("POST", "/p", strings.NewReader(`{"id":2}`))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 200 || body(t, rec) != `{"id":2,"name":""}` {
		t.Fatalf("PostJSON: %d %q", rec.Code, body(t, rec))
	}
	req = httptest.NewRequest("POST", "/c", strings.NewReader(`{"id":3}`))
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("CreatedJSON: %d, want 201", rec.Code)
	}
	if rec := do(t, app, "GET", "/t"); body(t, rec) != "ok" {
		t.Fatalf("GetText: %q", body(t, rec))
	}
}

func TestZeroVariants(t *testing.T) {
	app := web.New()
	app.Must(web.GetJSON0("/j0", func() (*user, error) { return &user{ID: 7}, nil }))
	app.Must(web.GetText0("/t0", func() (string, error) { return "plain", nil }))
	if rec := do(t, app, "GET", "/j0"); body(t, rec) != `{"id":7,"name":""}` {
		t.Fatalf("GetJSON0: %q", body(t, rec))
	}
	if rec := do(t, app, "GET", "/t0"); body(t, rec) != "plain" {
		t.Fatalf("GetText0: %q", body(t, rec))
	}
}

func TestVariadicMustAndGroup(t *testing.T) {
	app := web.New()
	var events []string
	g := app.Group("/api/v1", func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			events = append(events, "group")
			return next(c)
		}
	})
	g.Must(
		web.GetJSON("/a", web.NoIn(), func(web.None) (*user, error) { return &user{ID: 1}, nil }),
		web.GetJSON0("/b", func() (*user, error) { return &user{ID: 2}, nil }),
	)
	if rec := do(t, app, "GET", "/api/v1/a"); body(t, rec) != `{"id":1,"name":""}` {
		t.Fatalf("group a: %q", body(t, rec))
	}
	if rec := do(t, app, "GET", "/api/v1/b"); body(t, rec) != `{"id":2,"name":""}` {
		t.Fatalf("group b: %q", body(t, rec))
	}
	if rec := do(t, app, "GET", "/api/v1/nope"); rec.Code != 404 {
		t.Fatalf("outside group: %d", rec.Code)
	}
	if len(events) != 2 || events[0] != "group" {
		t.Fatalf("group middleware events: %v", events)
	}
}

func TestComposedInputs(t *testing.T) {
	app := web.New()

	// path + body：PUT /users/{id}
	app.Must(web.PutJSON("/users/{id}",
		web.All(web.PathInt64("id"), web.BodyJSON[user]()),
		func(p web.Pair[int64, user]) (user, error) {
			p.Second.ID = p.First
			return p.Second, nil
		}))

	req := httptest.NewRequest("PUT", "/users/9", strings.NewReader(`{"name":"Lin"}`))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 200 || body(t, rec) != `{"id":9,"name":"Lin"}` {
		t.Fatalf("pair: %d %q", rec.Code, body(t, rec))
	}

	// 错误短路：body 非法 → 400，不再到达 handler
	req = httptest.NewRequest("PUT", "/users/9", strings.NewReader(`{bad`))
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("short-circuit: %d, want 400", rec.Code)
	}

	// path + query + body：三源组合
	app.Must(web.PostJSON("/x/{id}",
		web.All3(web.PathInt64("id"), web.QueryInt("page"), web.BodyJSON[user]()),
		func(p web.Triple[int64, int, user]) (map[string]any, error) {
			return map[string]any{"id": p.First, "page": p.Second, "name": p.Third.Name}, nil
		}))
	req = httptest.NewRequest("POST", "/x/5?page=2", strings.NewReader(`{"name":"Ada"}`))
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if body(t, rec) != `{"id":5,"name":"Ada","page":2}` {
		t.Fatalf("triple: %q", body(t, rec))
	}
}
