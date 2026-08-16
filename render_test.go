package web

import (
	"io"
	"net/http"
	"testing"
)

// headerRecorder counts WriteHeader calls: the hijacked path must never
// touch it (net/http would log "response.WriteHeader on hijacked connection").
type headerRecorder struct {
	http.ResponseWriter
	calls int
}

func (r *headerRecorder) WriteHeader(int)             { r.calls++ }
func (r *headerRecorder) Write(p []byte) (int, error) { return len(p), nil }

// spyRenderer records whether WriteBody ran.
type spyRenderer struct{ wrote bool }

func (s *spyRenderer) ContentType() string { return "text/plain" }
func (s *spyRenderer) StatusCode() int     { return http.StatusOK }
func (s *spyRenderer) WriteBody(w io.Writer, v string) error {
	s.wrote = true
	return nil
}

// 问题 1 回归：WebSocket 升级后 render 必须完全跳过 HTTP 响应提交，
// 既不 WriteHeader（触发 net/http 的 hijacked 日志），也不 WriteBody
// （升级后 ResponseWriter.Write 直达原始连接，会污染帧流）。
func TestRenderHijackedSkipsCommit(t *testing.T) {
	rec := &headerRecorder{}
	c := &Ctx{W: rec, status: http.StatusOK, hijacked: true}
	if err := render(c, None{}, Upgraded[None]()); err != nil {
		t.Fatalf("render: %v", err)
	}
	if rec.calls != 0 {
		t.Fatalf("WriteHeader called %d times on hijacked connection", rec.calls)
	}
}

// 误配契约的防线：劫持后即使给了非 Upgraded 渲染器，也不执行
// WriteBody——把 HTTP 字节写进 WebSocket 帧流是协议污染。
func TestRenderHijackedSkipsBody(t *testing.T) {
	spy := &spyRenderer{}
	rec := &headerRecorder{}
	c := &Ctx{W: rec, status: http.StatusOK, hijacked: true}
	if err := render(c, "x", Renderer[string](spy)); err != nil {
		t.Fatalf("render: %v", err)
	}
	if spy.wrote {
		t.Fatal("WriteBody ran on hijacked connection")
	}
	if rec.calls != 0 {
		t.Fatalf("WriteHeader called %d times on hijacked connection", rec.calls)
	}
}
