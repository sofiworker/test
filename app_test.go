package web_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	web "example.com/web"
	"example.com/web/httperr"
)

type user struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func do(t *testing.T, app http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func body(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	b, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMountRenderText(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "home", nil }))
	rec := do(t, app, "GET", "/")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || body(t, rec) != "home" {
		t.Fatalf("%d %q %q", rec.Code, rec.Header().Get("Content-Type"), body(t, rec))
	}
}

func TestMountRenderJSON(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/u"), web.NoIn(), web.JSON[*user](),
		func(web.None) (*user, error) { return &user{ID: 1, Name: "Ada"}, nil }))
	rec := do(t, app, "GET", "/u")
	if rec.Code != 200 || body(t, rec) != `{"id":1,"name":"Ada"}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestRendererVariants(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/created"), web.NoIn(), web.Status(201, web.JSON[user]()),
		func(web.None) (user, error) { return user{ID: 2, Name: "Grace"}, nil }))
	app.Must(web.Handle(web.Get("/nocontent"), web.NoIn(), web.NoContent[web.None](),
		func(web.None) (web.None, error) { return web.None{}, nil }))
	app.Must(web.Handle(web.Get("/redir"), web.NoIn(), web.Redirect[web.None](302, "/login"),
		func(web.None) (web.None, error) { return web.None{}, nil }))

	rec := do(t, app, "GET", "/created")
	if rec.Code != 201 || body(t, rec) != `{"id":2,"name":"Grace"}` {
		t.Fatalf("created: %d %q", rec.Code, body(t, rec))
	}
	rec = do(t, app, "GET", "/nocontent")
	if rec.Code != 204 || body(t, rec) != "" {
		t.Fatalf("nocontent: %d %q", rec.Code, body(t, rec))
	}
	rec = do(t, app, "GET", "/redir")
	if rec.Code != 302 || rec.Header().Get("Location") != "/login" {
		t.Fatalf("redir: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestPathInt64Input(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/users/{id}"), web.PathInt64("id"), web.JSON[*user](),
		func(id int64) (*user, error) { return &user{ID: id}, nil }))
	rec := do(t, app, "GET", "/users/42")
	if rec.Code != 200 || body(t, rec) != `{"id":42,"name":""}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
	rec = do(t, app, "GET", "/users/abc")
	if rec.Code != 400 || body(t, rec) != `{"error":"bad request"}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestInFuncMultiInput(t *testing.T) {
	type in struct {
		ID  int64  `json:"id"`
		Tag string `json:"tag"`
	}
	inDesc := web.InFunc(func(r web.Req) (in, error) {
		id, err := r.Path().Int64("id")
		if err != nil {
			return in{}, httperr.BadRequest(err)
		}
		return in{ID: id, Tag: r.Query().String("tag")}, nil
	})
	app := web.New()
	app.Must(web.Handle(web.Get("/x/{id}"), inDesc, web.JSON[in](),
		func(v in) (in, error) { return v, nil }))
	rec := do(t, app, "GET", "/x/7?tag=red")
	if rec.Code != 200 || body(t, rec) != `{"id":7,"tag":"red"}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestErrorMapping(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/nf"), web.NoIn(), web.JSON[*user](),
		func(web.None) (*user, error) { return nil, httperr.NotFound() }))
	app.Must(web.Handle(web.Get("/boom"), web.NoIn(), web.JSON[*user](),
		func(web.None) (*user, error) { return nil, errors.New("kaboom") }))

	rec := do(t, app, "GET", "/nf")
	if rec.Code != 404 || body(t, rec) != `{"error":"not found"}` {
		t.Fatalf("nf: %d %q", rec.Code, body(t, rec))
	}
	rec = do(t, app, "GET", "/boom")
	if rec.Code != 500 || body(t, rec) != `{"error":"internal server error"}` {
		t.Fatalf("boom: %d %q", rec.Code, body(t, rec))
	}
}

func TestMiddlewareOrderAndShortCircuit(t *testing.T) {
	app := web.New()
	var events []string
	app.Use(
		func(next web.Handler) web.Handler {
			return func(c *web.Ctx) error {
				events = append(events, "m1-in")
				err := next(c)
				events = append(events, "m1-out")
				return err
			}
		},
		func(next web.Handler) web.Handler {
			return func(c *web.Ctx) error {
				events = append(events, "m2-in")
				err := next(c)
				events = append(events, "m2-out")
				return err
			}
		},
	)
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) {
			events = append(events, "h")
			return "ok", nil
		}))
	do(t, app, "GET", "/x")
	want := "m1-in,m2-in,h,m2-out,m1-out"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}

	// Short-circuit: auth middleware that never calls next.
	app2 := web.New()
	var hit bool
	app2.Use(func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error { return httperr.Unauthorized() }
	})
	app2.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { hit = true; return "ok", nil }))
	rec := do(t, app2, "GET", "/x")
	if rec.Code != 401 || hit {
		t.Fatalf("short-circuit: %d hit=%v", rec.Code, hit)
	}
}

func TestRouteMiddlewareOrder(t *testing.T) {
	var events []string
	app := web.New()
	app.Use(func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			events = append(events, "global")
			return next(c)
		}
	})
	route := web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "ok", nil }).
		With(func(next web.Handler) web.Handler {
			return func(c *web.Ctx) error {
				events = append(events, "route")
				return next(c)
			}
		})
	app.Must(route)
	do(t, app, "GET", "/x")
	if got := strings.Join(events, ","); got != "global,route" {
		t.Fatalf("order = %q, want global,route", got)
	}
}

func TestTypedKeys(t *testing.T) {
	reqID := web.NewKey[string]("request-id")
	app := web.New()
	app.Use(func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			reqID.Set(c, "abc-123")
			return next(c)
		}
	})
	// Scoped values are part of the input: the constructor reads the typed
	// key, the handler receives it as part of its contract.
	type in struct{ id string }
	inDesc := web.InFunc(func(r web.Req) (in, error) {
		id, ok := reqID.Get(r.Raw())
		if !ok {
			return in{}, httperr.Unauthorized()
		}
		return in{id: id}, nil
	})
	app.Must(web.Handle(web.Get("/k"), inDesc, web.Text(),
		func(v in) (string, error) { return v.id, nil }))
	rec := do(t, app, "GET", "/k")
	if rec.Code != 200 || body(t, rec) != "abc-123" {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestHTTPBehavior(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "get", nil }))
	app.Must(web.Handle(web.Post("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "post", nil }))

	rec := do(t, app, "HEAD", "/x")
	if rec.Code != 200 || body(t, rec) != "get" {
		t.Fatalf("HEAD: %d %q", rec.Code, body(t, rec))
	}
	rec = do(t, app, "PUT", "/x")
	if rec.Code != 405 || rec.Header().Get("Allow") != "GET, OPTIONS, POST" {
		t.Fatalf("405: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	rec = do(t, app, "OPTIONS", "/x")
	if rec.Code != 204 || rec.Header().Get("Allow") != "GET, OPTIONS, POST" {
		t.Fatalf("OPTIONS: %d Allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	rec = do(t, app, "GET", "/nope")
	if rec.Code != 404 {
		t.Fatalf("404: %d", rec.Code)
	}
}

func TestRecoverMiddleware(t *testing.T) {
	app := web.New()
	app.Use(web.Recover())
	app.Must(web.Handle(web.Get("/panic"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { panic("boom") }))
	rec := do(t, app, "GET", "/panic")
	if rec.Code != 500 || body(t, rec) != `{"error":"internal server error"}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestConcurrentRequests(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/users/{id}"), web.PathInt64("id"), web.JSON[*user](),
		func(id int64) (*user, error) { return &user{ID: id}, nil }))
	srv := httptest.NewServer(app)
	defer srv.Close()

	done := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				resp, err := http.Get(srv.URL + "/users/42")
				if err != nil {
					done <- err
					return
				}
				resp.Body.Close()
				if resp.StatusCode != 200 {
					done <- errors.New("bad status")
					return
				}
			}
			done <- nil
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRouteReuse(t *testing.T) {
	// One contract, many implementations, many apps: endpoints are data.
	contract := web.WithOut(web.WithIn(web.Get("/who"), web.NoIn()), web.Text())
	appA := web.New()
	appA.Must(contract.Handle(func(web.None) (string, error) { return "A", nil }))
	appB := web.New()
	appB.Must(contract.Handle(func(web.None) (string, error) { return "B", nil }))
	if body(t, do(t, appA, "GET", "/who")) != "A" {
		t.Fatal("appA")
	}
	if body(t, do(t, appB, "GET", "/who")) != "B" {
		t.Fatal("appB")
	}
}

func TestMountErrors(t *testing.T) {
	app := web.New()
	if err := app.Mount(nil); err == nil {
		t.Fatal("nil route must error")
	}
	bad := web.Handle(web.Get("/x/"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "", nil })
	if err := app.Mount(bad); err == nil || !strings.Contains(err.Error(), "must not end with") {
		t.Fatalf("trailing slash: %v", err)
	}
	ok := web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "", nil })
	if err := app.Mount(ok); err != nil {
		t.Fatal(err)
	}
	dup := web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "", nil })
	if err := app.Mount(dup); err == nil || !strings.Contains(err.Error(), "duplicate route") {
		t.Fatalf("duplicate: %v", err)
	}
}
