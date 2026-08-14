package web_test

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	web "example.com/web"
)

func TestBodyJSONValidation(t *testing.T) {
	app := web.New()
	validate := func(u user) error {
		if u.Name == "" {
			return errors.New("name is required")
		}
		return nil
	}
	app.Must(web.PostJSON("/users", web.BodyJSON[user](validate),
		func(u user) (user, error) { return u, nil }))

	req := httptest.NewRequest("POST", "/users", strings.NewReader(`{"id":1,"name":"Ada"}`))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("valid: %d", rec.Code)
	}

	// 校验失败 → 400，客户端看到校验器自己的原因
	req = httptest.NewRequest("POST", "/users", strings.NewReader(`{"id":2}`))
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(body(t, rec), "name is required") {
		t.Fatalf("invalid: %d %q", rec.Code, body(t, rec))
	}
}

func TestStreamRenderer(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/events"), web.NoIn(), web.Stream("text/event-stream"),
		func(web.None) (func(io.Writer) error, error) {
			return func(w io.Writer) error {
				_, err := io.WriteString(w, "data: tick\n\n")
				return err
			}, nil
		}))

	rec := do(t, app, "GET", "/events")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" || body(t, rec) != "data: tick\n\n" {
		t.Fatalf("%d %q %q", rec.Code, rec.Header().Get("Content-Type"), body(t, rec))
	}
}

func TestDeclaredResponses(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/users/{id}"), web.PathInt64("id"),
		web.Docs(web.JSON[*user](), map[string]*web.Response{
			"404": {Description: "User not found"},
		}),
		func(id int64) (*user, error) { return &user{ID: id}, nil }))

	doc := app.Doc(web.Info{Title: "d", Version: "1"})
	op := doc.Paths["/users/{id}"]["get"]
	if op == nil || op.Responses["404"] == nil || op.Responses["404"].Description != "User not found" {
		t.Fatalf("declared 404 missing: %+v", op)
	}
	// 运行时不受影响
	if rec := do(t, app, "GET", "/users/7"); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestPathFloat64AndQueryBool(t *testing.T) {
	app := web.New()
	app.Must(web.GetJSON("/r/{ratio}", web.PathFloat64("ratio", web.Min(0), web.Max(1)),
		func(v float64) (map[string]float64, error) { return map[string]float64{"ratio": v}, nil }))
	app.Must(web.GetJSON("/f", web.QueryBool("verbose"),
		func(v bool) (map[string]bool, error) { return map[string]bool{"verbose": v}, nil }))

	if rec := do(t, app, "GET", "/r/0.5"); rec.Code != 200 {
		t.Fatalf("ratio ok: %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/r/1.5"); rec.Code != 400 {
		t.Fatalf("ratio over max: %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/f?verbose=true"); body(t, rec) != `{"verbose":true}` {
		t.Fatalf("bool: %q", body(t, rec))
	}
	if rec := do(t, app, "GET", "/f?verbose=yes"); rec.Code != 400 {
		t.Fatalf("bad bool: %d", rec.Code)
	}
}
