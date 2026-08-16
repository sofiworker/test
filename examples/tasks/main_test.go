package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	httperr "example.com/web/httperr"
)

func newTestServer(t *testing.T) (*store, http.Handler) {
	t.Helper()
	db := &store{tasks: map[int64]task{}}
	return db, newApp(db)
}

func do(t *testing.T, h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func TestAuthRequired(t *testing.T) {
	_, h := newTestServer(t)
	if rec := do(t, h, "GET", "/tasks", "", ""); rec.Code != 401 {
		t.Fatalf("no token: %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/tasks", "", "wrong"); rec.Code != 401 {
		t.Fatalf("bad token: %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/whoami", "", "dev-token"); rec.Body.String() != "dev" {
		t.Fatalf("whoami: %q", rec.Body.String())
	}
}

func TestTaskLifecycle(t *testing.T) {
	_, h := newTestServer(t)

	// 创建（校验器拦截空标题）
	if rec := do(t, h, "POST", "/tasks", `{"title":""}`, "dev-token"); rec.Code != 400 {
		t.Fatalf("empty title: %d, want 400", rec.Code)
	}
	rec := do(t, h, "POST", "/tasks", `{"title":"ship it"}`, "dev-token")
	if rec.Code != 201 {
		t.Fatalf("create: %d", rec.Code)
	}
	var created task
	decode(t, rec, &created)
	if created.ID != 1 || created.Status != "open" {
		t.Fatalf("created = %+v", created)
	}

	// 读取 / 404
	if rec := do(t, h, "GET", "/tasks/1", "", "dev-token"); rec.Code != 200 {
		t.Fatalf("get: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/tasks/9", "", "dev-token"); rec.Code != 404 {
		t.Fatalf("get 404: %d", rec.Code)
	}

	// path+body 组合更新
	rec = do(t, h, "PUT", "/tasks/1", `{"title":"shipped it"}`, "dev-token")
	if rec.Code != 200 {
		t.Fatalf("update: %d", rec.Code)
	}
	var updated task
	decode(t, rec, &updated)
	if updated.Title != "shipped it" {
		t.Fatalf("updated = %+v", updated)
	}

	// 状态机：done 幂等冲突
	if rec := do(t, h, "POST", "/tasks/1/done", "", "dev-token"); rec.Code != 200 {
		t.Fatalf("done: %d", rec.Code)
	}
	if rec := do(t, h, "POST", "/tasks/1/done", "", "dev-token"); rec.Code != 409 {
		t.Fatalf("double done: %d, want 409", rec.Code)
	}

	// 删除 → 404
	if rec := do(t, h, "DELETE", "/tasks/1", "", "dev-token"); rec.Code != 204 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/tasks/1", "", "dev-token"); rec.Code != 404 {
		t.Fatalf("after delete: %d", rec.Code)
	}
}

func TestPaginationAndFilter(t *testing.T) {
	_, h := newTestServer(t)
	for i := 0; i < 5; i++ {
		do(t, h, "POST", "/tasks", `{"title":"t"}`, "dev-token")
	}
	do(t, h, "POST", "/tasks/1/done", "", "dev-token")

	rec := do(t, h, "GET", "/tasks/?page=1&size=2", "", "dev-token")
	var l taskList
	decode(t, rec, &l)
	if l.Total != 5 || len(l.Items) != 2 || l.Page != 1 {
		t.Fatalf("list = %+v", l)
	}

	rec = do(t, h, "GET", "/tasks/?status=done", "", "dev-token")
	decode(t, rec, &l)
	if l.Total != 1 || l.Items[0].Status != "done" {
		t.Fatalf("filtered = %+v", l)
	}

	if rec := do(t, h, "GET", "/tasks/?page=0", "", "dev-token"); rec.Code != 400 {
		t.Fatalf("bad page: %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/tasks/?status=weird", "", "dev-token"); rec.Code != 400 {
		t.Fatalf("bad status: %d", rec.Code)
	}
}

func TestOpenAPIDoc(t *testing.T) {
	_, h := newTestServer(t)
	rec := do(t, h, "GET", "/openapi.json", "", "")
	if rec.Code != 200 {
		t.Fatalf("openapi: %d", rec.Code)
	}
	s := rec.Body.String()
	for _, want := range []string{`"/tasks/{id}"`, `"title":"tasks API"`, `"requestBody"`} {
		if !strings.Contains(s, want) {
			t.Errorf("doc missing %s", want)
		}
	}
}

func TestProblemJSONDisabledByDefault(t *testing.T) {
	_, h := newTestServer(t)
	rec := do(t, h, "GET", "/tasks/9", "", "dev-token")
	if rec.Code != 404 || rec.Body.String() != `{"error":"not found"}` {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	_ = httperr.NotFound
}

func TestChatBroadcast(t *testing.T) {
	_, h := newTestServer(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/chat"
	a, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// 同步屏障：服务端在 hub.join 之后才发送 "joined" 确认，客户端
	// 读到它才保证自己已在广播名单内。Dial 返回只代表握手完成，
	// join 尚未执行——不等屏障直接发言是时序竞态（低概率漏收）。
	for _, conn := range []*websocket.Conn{a, b} {
		if err := waitJoined(conn); err != nil {
			t.Fatal(err)
		}
	}

	if err := a.WriteMessage(websocket.TextMessage, []byte("hello-all")); err != nil {
		t.Fatal(err)
	}
	b.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, msg, err := b.ReadMessage()
	if err != nil {
		t.Fatalf("broadcast read: %v", err)
	}
	if mt != websocket.TextMessage || string(msg) != "hello-all" {
		t.Fatalf("broadcast: %q", msg)
	}
}

// waitJoined 读到服务端的 "joined" 加入确认即返回，跳过确认之前可能
// 排队的其它消息。
func waitJoined(conn *websocket.Conn) error {
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if string(msg) == "joined" {
			return nil
		}
	}
}

// syncWriter is a thread-safe recorder for streaming tests.
type syncWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
}

func newSyncWriter() *syncWriter {
	return &syncWriter{header: http.Header{}}
}

func (s *syncWriter) Header() http.Header { return s.header }
func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *syncWriter) WriteHeader(int) {}
func (s *syncWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestSSETickerCancellation(t *testing.T) {
	_, h := newTestServer(t)
	req := httptest.NewRequest("GET", "/events/ticker", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := newSyncWriter()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()
	// 读到第一帧后取消上下文，会话应优雅退出
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.String(), "event: tick") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE session did not exit on cancellation")
	}
}
