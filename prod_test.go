package web_test

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	web "example.com/web"
	"example.com/web/httperr"
)

func TestTrustedProxies(t *testing.T) {
	app := web.New()
	app.Use(web.TrustedProxies("10.0.0.0/8"))
	type in struct{ ip string }
	desc := web.InFunc(func(r web.Req) (in, error) { return in{ip: r.ClientIP()}, nil })
	app.Must(web.GetText("/ip", desc, func(v in) (string, error) { return v.ip, nil }))

	// 受信代理转发
	req := httptest.NewRequest("GET", "/ip", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if body(t, rec) != "203.0.113.9" {
		t.Fatalf("xff: %q", body(t, rec))
	}

	// 不可信来源：伪造头被忽略
	req = httptest.NewRequest("GET", "/ip", nil)
	req.RemoteAddr = "198.51.100.7:9999"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if body(t, rec) != "198.51.100.7" {
		t.Fatalf("untrusted: %q", body(t, rec))
	}

	// 无中间件时 ClientIP = RemoteAddr
	app2 := web.New()
	app2.Must(web.GetText("/ip", desc, func(v in) (string, error) { return v.ip, nil }))
	req = httptest.NewRequest("GET", "/ip", nil)
	req.RemoteAddr = "192.0.2.1:55"
	rec = httptest.NewRecorder()
	app2.ServeHTTP(rec, req)
	if body(t, rec) != "192.0.2.1" {
		t.Fatalf("direct: %q", body(t, rec))
	}
}

func TestDownloadAndStreamFile(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/dl"), web.NoIn(), web.Download("report.csv"),
		func(web.None) ([]byte, error) { return []byte("a,b\n1,2\n"), nil }))
	rec := do(t, app, "GET", "/dl")
	if rec.Header().Get("Content-Disposition") != `attachment; filename="report.csv"` || body(t, rec) != "a,b\n1,2\n" {
		t.Fatalf("%q %q", rec.Header().Get("Content-Disposition"), body(t, rec))
	}

	f := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(f, []byte("streamed-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.Must(web.Handle(web.Get("/file"), web.NoIn(), web.StreamFile("application/octet-stream"),
		func(web.None) (io.ReadSeeker, error) { return os.Open(f) }))
	rec = do(t, app, "GET", "/file")
	if body(t, rec) != "streamed-content" {
		t.Fatalf("stream: %q", body(t, rec))
	}
}

func TestSPA(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644)

	app := web.New()
	app.Must(web.SPA("/", dir)...)
	if body(t, do(t, app, "GET", "/")) != "<html>app</html>" {
		t.Fatalf("root fallback failed")
	}
	if body(t, do(t, app, "GET", "/assets/app.js")) != "console.log(1)" {
		t.Fatalf("asset failed")
	}
	if body(t, do(t, app, "GET", "/some/deep/route")) != "<html>app</html>" {
		t.Fatalf("deep fallback failed")
	}
}

func TestHealthcheckAndProblemInstance(t *testing.T) {
	app := web.New()
	app.UseProblemJSON()
	app.Must(web.Healthcheck())
	app.Must(web.Handle(web.Get("/boom"), web.NoIn(), web.JSON[*user](), func(web.None) (*user, error) { return nil, httperr.NotFound() }))
	if body(t, do(t, app, "GET", "/healthz")) != `{"status":"ok"}` {
		t.Fatalf("healthcheck")
	}
	b := body(t, do(t, app, "GET", "/boom"))
	if !strings.Contains(b, `"instance":"/boom"`) {
		t.Fatalf("problem instance: %q", b)
	}
}
