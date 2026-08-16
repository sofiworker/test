package web_test

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"

	web "example.com/web"
	"example.com/web/httperr"
)

// 覆盖收尾：把 <100% 的关键分支补齐。

func TestMustPanicsOnConflict(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "", nil }))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Must should panic on duplicate route")
		}
	}()
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "", nil }))
}

func TestHandlerAccessor(t *testing.T) {
	app := web.New()
	if app.Handler() == nil {
		t.Fatal("Handler() returned nil")
	}
}

func TestGroupMountNilAndNestedPrefix(t *testing.T) {
	app := web.New()
	g := app.Group("/api")
	if err := g.Mount(nil); err == nil {
		t.Fatal("nil route must error")
	}
	g2 := g.Group("/v2")
	g2.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "nested", nil }))
	if body(t, do(t, app, "GET", "/api/v2/x")) != "nested" {
		t.Fatal("nested group prefix")
	}
}

func TestGroupPanicsOnBadPrefix(t *testing.T) {
	app := web.New()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("bad prefix should panic")
		}
	}()
	app.Group("nope")
}

func TestFromStdFuncAndStaticRoot(t *testing.T) {
	app := web.New()
	app.Must(web.Raw("GET", "/std", web.FromStdFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("std-fn"))
	})))
	if body(t, do(t, app, "GET", "/std")) != "std-fn" {
		t.Fatal("FromStdFunc")
	}
}

func TestStatusWrittenBeforeError(t *testing.T) {
	// handler 先写状态再返回错误：writeError 跳过、状态保留
	app := web.New()
	app.Must(web.Raw("GET", "/x", web.Handler(func(c *web.Ctx) error {
		c.WriteHeader(418)
		c.Write([]byte("teapot"))
		return httperr.NotFound()
	})))
	rec := do(t, app, "GET", "/x")
	if rec.Code != 418 {
		t.Fatalf("status kept: %d", rec.Code)
	}
}

func TestMultiSetCookieChain(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/c"), web.NoIn(),
		web.SetCookie(web.SetCookie(web.Text(), &http.Cookie{Name: "a", Value: "1"}),
			&http.Cookie{Name: "b", Value: "2"}),
		func(web.None) (string, error) { return "ok", nil }))
	rec := do(t, app, "GET", "/c")
	h := rec.Header()
	if len(h["Set-Cookie"]) != 2 {
		t.Fatalf("set-cookie chain: %v", h["Set-Cookie"])
	}
}

func TestQueryIntDefaultFallbackAndInvalid(t *testing.T) {
	app := web.New()
	app.Must(web.GetJSON("/d", web.QueryIntDefault("page", 7),
		func(v int) (int, error) { return v, nil }))
	if body(t, do(t, app, "GET", "/d")) != "7" {
		t.Fatal("default")
	}
	if body(t, do(t, app, "GET", "/d?page=xyz")) != "7" {
		t.Fatal("invalid fallback")
	}
}

func TestWildcardBracesRejected(t *testing.T) {
	app := web.New()
	if err := app.Mount(web.Raw("GET", "/a/{x}y", web.Handler(func(c *web.Ctx) error { return nil }))); err == nil {
		t.Fatal("braces in name should error")
	}
}

func TestTrustedProxiesInvalidCIDRPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("invalid cidr should panic")
		}
	}()
	web.TrustedProxies("not-a-cidr")
}

func TestLoggerSlogZeroOpts(t *testing.T) {
	// LoggerOpts 零值可用（Skip==nil 分支）
	app := web.New()
	app.Use(web.LoggerSlog(slog.New(slog.NewTextHandler(io.Discard, nil)), web.LoggerOpts{}))
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "ok", nil }))
	if rec := do(t, app, "GET", "/x"); rec.Code != 200 {
		t.Fatal("zero opts")
	}
}

func TestSPAStaticPrefixSubdir(t *testing.T) {
	// SPA 非根前缀
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<html>home</html>")
	app := web.New()
	app.Must(web.SPA("/app/", dir)...)
	if body(t, do(t, app, "GET", "/app")) != "<html>home</html>" {
		t.Fatal("bare prefix")
	}
	if body(t, do(t, app, "GET", "/app/deep")) != "<html>home</html>" {
		t.Fatal("deep fallback")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
