package web

import (
	"io"
	"net/http"

	"github.com/gorilla/websocket"
)

// WebSocket 支持（采纳 gorilla/websocket，组织复活后的官方维护版）：
// 升级是输入契约（WSConn/UpgradeWS 描述器），升级后不写响应是输出契约
// （Upgraded 渲染器）。handler 拥有会话循环：读、写、关闭都由它决定，
// 框架在升级后不再触碰响应流。

// DefaultWSUpgrader is the shared upgrader used by WSConn. Production code
// should configure CheckOrigin (origin allow-list) and buffer sizes.
var DefaultWSUpgrader = &websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(*http.Request) bool { return true }, // 默认放行；生产必须收紧
}

// UpgradeWS upgrades the request to WebSocket through u and hands the
// connection to the handler. On failure the error flows through the normal
// error pipeline (upgrader has written nothing yet, so a 400 is rendered).
func UpgradeWS(u *websocket.Upgrader) In[*websocket.Conn] {
	return InFunc(func(r Req) (*websocket.Conn, error) {
		conn, err := u.Upgrade(r.Raw().W, r.Raw().Req, nil)
		if err != nil {
			return nil, err
		}
		r.Raw().hijacked = true
		return conn, nil
	})
}

// WSConn upgrades with the DefaultWSUpgrader.
func WSConn() In[*websocket.Conn] { return UpgradeWS(DefaultWSUpgrader) }

// Upgraded renders nothing: the connection was already upgraded (101) and
// the response stream belongs to the upgraded protocol. Pair it with
// UpgradeWS/WSConn; the handler's return value is ignored. Once UpgradeWS
// has hijacked the connection, render() skips ALL HTTP response commits
// (status, headers and body) — only the handler's own connection writes
// reach the wire.
func Upgraded[O any]() Renderer[O] { return upgradedRenderer[O]{} }

type upgradedRenderer[O any] struct{}

func (upgradedRenderer[O]) ContentType() string          { return "" }
func (upgradedRenderer[O]) StatusCode() int              { return http.StatusSwitchingProtocols }
func (upgradedRenderer[O]) WriteBody(io.Writer, O) error { return nil }
