package web_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
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
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.JSON[*user](), func(web.None) (*user, error) { return &user{ID: 1}, nil }))

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

func TestCompressMiddleware(t *testing.T) {
	app := web.New()
	app.Use(web.Compress())
	app.Must(web.GetText("/big", web.NoIn(),
		func(web.None) (string, error) { return strings.Repeat("hello ", 100), nil }))

	req := httptest.NewRequest("GET", "/big", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("not gzipped: %q", rec.Header().Get("Content-Encoding"))
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(gz)
	if !strings.HasPrefix(string(raw), "hello hello") {
		t.Fatalf("bad content: %q", string(raw)[:20])
	}
	// 无 Accept-Encoding → 原样
	if rec2 := do(t, app, "GET", "/big"); rec2.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unexpected gzip: %q", rec2.Header().Get("Content-Encoding"))
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	app := web.New()
	app.Use(web.RateLimit(100, 2, nil)) // 每秒 100、桶容量 2：前两个立刻放行，第三个 429
	app.Must(web.GetText("/x", web.NoIn(), func(web.None) (string, error) { return "ok", nil }))
	req := httptest.NewRequest("GET", "/x", nil)
	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("codes = %v, want [200 200 429]", codes)
	}
}

func TestCacheHeaders(t *testing.T) {
	app := web.New()
	app.Must(web.GetText("/c", web.NoIn(), func(web.None) (string, error) { return "x", nil }).
		With(web.CacheControl(300)))
	app.Must(web.GetText("/n", web.NoIn(), func(web.None) (string, error) { return "x", nil }).
		With(web.NoCache()))
	if rec := do(t, app, "GET", "/c"); rec.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("%q", rec.Header().Get("Cache-Control"))
	}
	if rec := do(t, app, "GET", "/n"); rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%q", rec.Header().Get("Cache-Control"))
	}
}

func TestSSEHelper(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/events"), web.NoIn(), web.SSE(),
		func(web.None) (func(*web.SSEWriter) error, error) {
			return func(s *web.SSEWriter) error {
				if err := s.Event("update").Data(map[string]int{"n": 1}); err != nil {
					return err
				}
				return s.Ping()
			}, nil
		}))
	rec := do(t, app, "GET", "/events")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if b := body(t, rec); !strings.Contains(b, "event: update\n") || !strings.Contains(b, `data: {"n":1}`) || !strings.Contains(b, ": ping") {
		t.Fatalf("bad SSE output: %q", b)
	}
}

func TestNestedGroupAndAccessors(t *testing.T) {
	app := web.New()
	var events []string
	v1 := app.Group("/api", func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			events = append(events, "api")
			return next(c)
		}
	})
	users := v1.Group("/users", func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			events = append(events, "users")
			return next(c)
		}
	})
	users.Must(web.GetJSON("/{id}", web.PathInt64("id"),
		func(id int64) (*user, error) { return &user{ID: id}, nil }))

	if rec := do(t, app, "GET", "/api/users/7"); body(t, rec) != `{"id":7,"name":""}` {
		t.Fatalf("nested group: %q", body(t, rec))
	}
	if len(events) != 2 || events[0] != "api" || events[1] != "users" {
		t.Fatalf("middleware order: %v", events)
	}

	// 路由元数据访问器
	route := web.GetJSON("/meta", web.NoIn(), func(web.None) (*user, error) { return nil, nil })
	if route.Method() != "GET" || route.Path() != "/meta" {
		t.Fatalf("%s %s", route.Method(), route.Path())
	}
}

func TestLoggerSlogStructured(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)
	l := slog.New(h)
	reqID := web.NewKey[string]("rid")
	app := web.New()
	app.Use(web.RequestID(reqID, web.NewID), web.LoggerSlog(l, web.LoggerOpts{RequestIDKey: reqID}))
	app.Must(web.Handle(web.Get("/ok"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "ok", nil }))
	app.Must(web.Handle(web.Get("/bad"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "", httperr.NotFound() }))
	app.Must(web.Handle(web.Get("/skip"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "ok", nil }))

	do(t, app, "GET", "/ok")
	do(t, app, "GET", "/bad")
	logs := buf.String()
	if !strings.Contains(logs, `method=GET`) || !strings.Contains(logs, `request_id=`) {
		t.Fatalf("access log shape: %q", logs)
	}
	if !strings.Contains(logs, "status=404") {
		t.Fatalf("effective status missing: %q", logs)
	}
}

func TestLoggerSlogSkip(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	app := web.New()
	app.Use(web.LoggerSlog(l, web.LoggerOpts{
		Skip: func(c *web.Ctx) bool { return c.Path() == "/healthz" },
	}))
	app.Must(web.Handle(web.Get("/healthz"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "ok", nil }))
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "ok", nil }))
	do(t, app, "GET", "/healthz")
	do(t, app, "GET", "/x")
	logs := buf.String()
	if strings.Contains(logs, "/healthz") {
		t.Fatalf("healthz should be skipped: %q", logs)
	}
	if !strings.Contains(logs, "/x") {
		t.Fatalf("x should be logged: %q", logs)
	}
}

func TestRecoverSlog(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	app := web.New()
	app.Use(web.RecoverSlog(l))
	app.Must(web.Handle(web.Get("/panic"), web.NoIn(), web.Text(), func(web.None) (string, error) { panic("boom") }))
	rec := do(t, app, "GET", "/panic")
	if rec.Code != 500 || !strings.Contains(buf.String(), "boom") {
		t.Fatalf("%d %q", rec.Code, buf.String())
	}
}

func TestSecureHeaders(t *testing.T) {
	app := web.New()
	app.Use(web.SecureHeaders())
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "ok", nil }))
	rec := do(t, app, "GET", "/x")
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("X-Frame-Options") != "DENY" ||
		rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("%v", rec.Header())
	}
}

func TestSlogContextMiddleware(t *testing.T) {
	logKey := web.NewKey[*slog.Logger]("logger")
	l := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := web.New()
	app.Use(web.SlogContext(logKey, l))
	type in struct{ hasLog bool }
	desc := web.InFunc(func(r web.Req) (in, error) {
		_, ok := logKey.Get(r.Raw())
		return in{hasLog: ok}, nil
	})
	app.Must(web.GetText("/x", desc, func(v in) (string, error) {
		if !v.hasLog {
			return "missing", nil
		}
		return "has", nil
	}))
	if body(t, do(t, app, "GET", "/x")) != "has" {
		t.Fatalf("logger not injected")
	}
}
