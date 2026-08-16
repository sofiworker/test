package web_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	web "example.com/web"
)

func TestWebSocketEcho(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/ws"),
		web.WSConn(),
		web.Upgraded[web.None](),
		func(conn *websocket.Conn) (web.None, error) {
			defer conn.Close()
			for {
				mt, msg, err := conn.ReadMessage()
				if err != nil {
					return web.None{}, nil // 客户端关闭
				}
				if err := conn.WriteMessage(mt, msg); err != nil {
					return web.None{}, err
				}
			}
		}))

	srv := httptest.NewServer(app)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v (resp: %+v)", err, resp)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if mt != websocket.TextMessage || string(msg) != "hello" {
		t.Fatalf("echo: %q", msg)
	}
}

func TestWebSocketUpgradeFailure(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/ws"),
		web.WSConn(),
		web.Upgraded[web.None](),
		func(conn *websocket.Conn) (web.None, error) { return web.None{}, nil }))

	// 普通 GET（无 Upgrade 头）→ 升级失败 → 错误管道
	rec := do(t, app, "GET", "/ws")
	if rec.Code != 400 {
		t.Fatalf("non-ws request: %d, want 400", rec.Code)
	}
}

func TestSSEFullFields(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/events"), web.NoIn(), web.SSE(),
		func(web.None) (func(*web.SSEWriter) error, error) {
			return func(s *web.SSEWriter) error {
				if err := s.Comment("welcome"); err != nil {
					return err
				}
				if err := s.Event("tick").ID("42").Retry(3000).Data(map[string]int{"n": 1}); err != nil {
					return err
				}
				return s.Ping()
			}, nil
		}))
	rec := do(t, app, "GET", "/events")
	b := body(t, rec)
	for _, want := range []string{": welcome", "event: tick", "id: 42", "retry: 3000", "data: {\"n\":1}", ": ping"} {
		if !strings.Contains(b, want) {
			t.Errorf("SSE missing %q in:\n%s", want, b)
		}
	}
}

func TestPProfRoute(t *testing.T) {
	app := web.New()
	app.Must(web.PProf()...)
	if rec := do(t, app, "GET", "/debug/pprof/"); rec.Code != 200 {
		t.Fatalf("pprof index: %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/debug/pprof/cmdline"); rec.Code != 200 {
		t.Fatalf("pprof cmdline: %d", rec.Code)
	}
}

func TestAppServeGraceful(t *testing.T) {
	// 冒烟：启动真实端口、请求、关闭（信号路径在进程级测试，这里只验证 Serve 正常服务）
	app := web.New()
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(), func(web.None) (string, error) { return "ok", nil }))
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: app}
	ln, err := netListen(srv)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	resp, err := http.Get("http://" + ln.Addr().String() + "/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	srv.Close()
}

func netListen(srv *http.Server) (net.Listener, error) { return net.Listen("tcp", srv.Addr) }
