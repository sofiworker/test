package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	web "example.com/web"
	"example.com/web/httperr"
)

func TestRequestIDMiddleware(t *testing.T) {
	reqID := web.NewKey[string]("request-id")
	app := web.New()
	app.Use(web.RequestID(reqID, web.NewID))

	type in struct{ id string }
	desc := web.InFunc(func(r web.Req) (in, error) {
		id, ok := reqID.Get(r.Raw())
		if !ok {
			return in{}, httperr.Unauthorized()
		}
		return in{id: id}, nil
	})
	app.Must(web.GetText("/x", desc, func(v in) (string, error) { return v.id, nil }))

	// 无入站 id → 生成
	rec := do(t, app, "GET", "/x")
	if rec.Code != 200 || len(body(t, rec)) != 16 || rec.Header().Get("X-Request-Id") == "" {
		t.Fatalf("%d %q %q", rec.Code, body(t, rec), rec.Header().Get("X-Request-Id"))
	}
	// 入站 id → 透传
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-Id", "trace-42")
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if body(t, rr) != "trace-42" {
		t.Fatalf("passthrough: %q", body(t, rr))
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	app := web.New()
	app.Use(web.Timeout(30 * time.Millisecond))
	type in struct{ ctx context.Context }
	desc := web.InFunc(func(r web.Req) (in, error) { return in{ctx: r.Context()}, nil })
	app.Must(web.GetText("/slow", desc, func(v in) (string, error) {
		select {
		case <-v.ctx.Done():
			return "timed out", nil
		case <-time.After(time.Second):
			return "too slow", nil
		}
	}))
	if rec := do(t, app, "GET", "/slow"); body(t, rec) != "timed out" {
		t.Fatalf("%q", body(t, rec))
	}
}

func TestBodyLimitMiddleware(t *testing.T) {
	app := web.New()
	app.Use(web.BodyLimit(8))
	app.Must(web.PostJSON("/x", web.BodyJSON[user](),
		func(u user) (user, error) { return u, nil }))
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"id":1,"name":"very-long-name"}`))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("%d, want 400 (limit enforced through decode)", rec.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	app := web.New()
	app.UseCORS(web.CORSConfig{
		AllowOrigins:  []string{"https://app.example.com"},
		AllowMethods:  []string{"GET", "POST"},
		AllowHeaders:  []string{"Content-Type"},
		MaxAgeSeconds: 600,
	})
	app.Must(web.GetJSON0("/x", func() (*user, error) { return &user{ID: 1}, nil }))

	// 预检
	req := httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" ||
		rec.Header().Get("Access-Control-Allow-Methods") != "GET, POST" {
		t.Fatalf("preflight: %d %q", rec.Code, rec.Header())
	}
	// 简单请求打标
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("simple: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	// 陌生来源不打标
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin stamped: %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestWithHeaderContract(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/cached"), web.NoIn(),
		web.WithHeader(web.WithHeader(web.JSON[*user](), "X-Cache", "hit"), "X-Version", "1"),
		func(web.None) (*user, error) { return &user{ID: 1}, nil }))

	rec := do(t, app, "GET", "/cached")
	if rec.Header().Get("X-Cache") != "hit" || rec.Header().Get("X-Version") != "1" {
		t.Fatalf("headers: %q %q", rec.Header().Get("X-Cache"), rec.Header().Get("X-Version"))
	}
	doc := app.Doc(web.Info{Title: "h", Version: "1"})
	op := doc.Paths["/cached"]["get"]
	if op == nil || op.Responses["200"] == nil || op.Responses["200"].Headers["X-Cache"] == nil {
		t.Fatalf("response headers missing from doc: %+v", op)
	}
}

func TestFromStdAndStatic(t *testing.T) {
	app := web.New()
	// stdlib handler 适配
	app.Must(web.Raw("GET", "/std", web.FromStdFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("std"))
	})))
	if rec := do(t, app, "GET", "/std"); body(t, rec) != "std" {
		t.Fatalf("%q", body(t, rec))
	}

	// 静态文件
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.Must(web.Static("/assets/", dir))
	if rec := do(t, app, "GET", "/assets/app.css"); rec.Code != 200 || body(t, rec) != "body{}" {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
	if rec := do(t, app, "GET", "/assets/missing.css"); rec.Code != 404 {
		t.Fatalf("missing: %d", rec.Code)
	}
}

func TestServeRoute(t *testing.T) {
	route := web.GetJSON("/users/{id}", web.PathInt64("id"),
		func(id int64) (*user, error) { return &user{ID: id}, nil })
	rec := web.ServeRoute(route, httptest.NewRequest("GET", "/users/42", nil))
	if rec.Code != 200 || body(t, rec) != `{"id":42,"name":""}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}
