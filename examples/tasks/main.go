// tasks 是框架的完整样例：一个应用覆盖生产级能力的全景。
//
//	go run ./examples/tasks
//
// 覆盖：CRUD + 鉴权 + 分页/过滤 + 状态机 + 约束 + 组合（MapIn）+ 渲染全景
// （JSON/Text/XML/HTML 模板/下载/原始字节/文件流）+ 表单 + 上传 + 原始请求
// 逃生舱 + SSE + WebSocket + 静态文件/SPA + OpenAPI 文档 + 优雅停机 +
// 中间件电池（RequestID/Recover/Timeout/Compress/SecureHeaders/CORS）。
package main

import (
	"context"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	w "example.com/web"
	"example.com/web/httperr"
)

type task struct {
	ID     int64  `json:"id" xml:"id"`
	Title  string `json:"title" xml:"title"`
	Status string `json:"status" xml:"status"`
}

type taskList struct {
	Items []task `json:"items"`
	Total int    `json:"total"`
	Page  int    `json:"page"`
	Size  int    `json:"size"`
}

type createTask struct {
	Title string `json:"title"`
}

type updateTask struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

type store struct {
	mu    sync.Mutex
	next  int64
	tasks map[int64]task
}

func (s *store) create(title string) task {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	t := task{ID: s.next, Title: title, Status: "open"}
	s.tasks[t.ID] = t
	return t
}

func (s *store) get(id int64) (task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	return t, ok
}

func (s *store) update(id int64, u updateTask) (task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return task{}, httperr.NotFound().With("id", id)
	}
	if u.Title != "" {
		t.Title = u.Title
	}
	if u.Status == "open" || u.Status == "done" {
		t.Status = u.Status
	}
	s.tasks[id] = t
	return t, nil
}

func (s *store) done(id int64) (task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return task{}, httperr.NotFound().With("id", id)
	}
	if t.Status == "done" {
		return task{}, httperr.Conflict("task already done").With("id", id)
	}
	t.Status = "done"
	s.tasks[id] = t
	return t, nil
}

func (s *store) remove(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return httperr.NotFound().With("id", id)
	}
	delete(s.tasks, id)
	return nil
}

func (s *store) list(page, size int, status string) taskList {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []task
	for _, t := range s.tasks {
		if status != "" && t.Status != status {
			continue
		}
		items = append(items, t)
	}
	total := len(items)
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return taskList{Items: items[start:end], Total: total, Page: page, Size: size}
}

var userKey = w.NewKey[string]("user")

func requireAuth(next w.Handler) w.Handler {
	return func(c *w.Ctx) error {
		tok := strings.TrimPrefix(c.Req.Header.Get("Authorization"), "Bearer ")
		if tok != "dev-token" {
			return httperr.Unauthorized()
		}
		userKey.Set(c, "dev")
		return next(c)
	}
}

// chatHub 是 WebSocket 广播中心：所有客户端收所有消息。
type chatHub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]struct{}
}

func newChatHub() *chatHub {
	return &chatHub{clients: map[*websocket.Conn]struct{}{}}
}

func (h *chatHub) join(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *chatHub) leave(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

func (h *chatHub) broadcast(mt int, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if err := c.WriteMessage(mt, msg); err != nil {
			c.Close()
			delete(h.clients, c)
		}
	}
}

// updateInput 演示 MapIn：组合输入映射到自定义命名结构体。
type updateInput struct {
	ID   int64
	Body updateTask
}

func newApp(db *store) *w.App {
	app := w.New()

	// 中间件电池 + JSON 引擎可注入（默认标准库）
	// w.UseJSONCodec(自定义Marshal, 自定义Unmarshal) // 可选
	app.Use(
		w.RequestID(w.NewKey[string]("request-id"), w.NewID),
		w.Recover(),
		w.Timeout(5*time.Second),
		w.SecureHeaders(),
		w.Compress(),
		w.Logger(log.Default()),
	)
	app.UseCORS(w.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders: []string{"Authorization", "Content-Type"},
	})

	app.Must(w.Healthcheck())
	app.Must(w.Handle(w.Get("/openapi.json"), w.NoIn(), w.JSON[w.OpenAPIDoc](),
		func(w.None) (w.OpenAPIDoc, error) {
			return app.Doc(w.Info{Title: "tasks API", Version: "1.0.0"}), nil
		}))

	// ── 渲染全景：同一数据、不同输出契约 ─────────────────────────
	type profile struct {
		Name  string `json:"name" xml:"name"`
		Plans int    `json:"plans" xml:"plans"`
	}
	pageTmpl := template.Must(template.New("page").Parse("<h1>{{.Name}}</h1><p>plans: {{.Plans}}</p>"))

	app.Must(w.Handle(w.Get("/renders/text"), w.NoIn(), w.Text(),
		func(w.None) (string, error) { return "plain text", nil }))
	app.Must(w.Handle(w.Get("/renders/json"), w.NoIn(), w.JSON[profile](),
		func(w.None) (profile, error) { return profile{Name: "Ada", Plans: 3}, nil }))
	app.Must(w.Handle(w.Get("/renders/xml"), w.NoIn(), w.XML[profile](),
		func(w.None) (profile, error) { return profile{Name: "Ada", Plans: 3}, nil }))
	app.Must(w.Handle(w.Get("/renders/html"), w.NoIn(), w.HTML[profile](pageTmpl, "page"),
		func(w.None) (profile, error) { return profile{Name: "Ada", Plans: 3}, nil }))
	app.Must(w.Handle(w.Get("/renders/download"), w.NoIn(), w.Download("report.csv"),
		func(w.None) ([]byte, error) { return []byte("a,b\n1,2\n"), nil }))
	app.Must(w.Handle(w.Get("/renders/bytes"), w.NoIn(), w.Bytes("image/svg+xml"),
		func(w.None) ([]byte, error) { return []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), nil }))
	app.Must(w.Handle(w.Get("/renders/file"), w.NoIn(), w.StreamFile("text/plain"),
		func(w.None) (io.ReadSeeker, error) { return os.Open("go.mod") }))

	// ── 输入全景：表单 / 上传 / 原始请求逃生舱 ────────────────────
	app.Must(w.Handle(w.Post("/form"), w.FormValues(), w.JSON[url.Values](),
		func(v url.Values) (url.Values, error) { return v, nil }))
	app.Must(w.PostJSON("/upload", w.FormFile("file", 1<<20),
		func(u w.Upload) (map[string]any, error) {
			return map[string]any{"name": u.Name, "size": u.Size}, nil
		}))
	app.Must(w.Handle(w.Get("/whoami-raw"), w.RawRequest(), w.JSON[map[string]string](),
		func(r *http.Request) (map[string]string, error) {
			return map[string]string{"ua": r.Header.Get("User-Agent")}, nil
		}))

	// ── 实时全景：SSE（命名事件 + 心跳流）与 WebSocket（echo + 聊天广播）──
	// SSE 心跳流：循环由请求上下文驱动——客户端断开即优雅退出。
	tickerIn := w.InFunc(func(r w.Req) (context.Context, error) { return r.Context(), nil })
	app.Must(w.Handle(w.Get("/events"), w.NoIn(), w.SSE(),
		func(w.None) (func(*w.SSEWriter) error, error) {
			return func(s *w.SSEWriter) error {
				for i := 1; i <= 3; i++ {
					if err := s.Event("tick").ID(string(rune('0' + i))).Data(map[string]int{"n": i}); err != nil {
						return err
					}
				}
				return s.Ping()
			}, nil
		}))
	app.Must(w.Handle(w.Get("/events/ticker"), tickerIn, w.SSE(),
		func(ctx context.Context) (func(*w.SSEWriter) error, error) {
			return func(s *w.SSEWriter) error {
				t := time.NewTicker(time.Second)
				defer t.Stop()
				for i := 1; ; i++ {
					select {
					case <-ctx.Done():
						return nil // 客户端断开：退出会话
					case <-t.C:
						if err := s.Event("tick").Data(map[string]int{"n": i}); err != nil {
							return err
						}
					}
				}
			}, nil
		}))

	app.Must(w.Handle(w.Get("/ws"),
		w.WSConn(),
		w.Upgraded[w.None](),
		func(conn *websocket.Conn) (w.None, error) {
			defer conn.Close()
			for {
				mt, msg, err := conn.ReadMessage()
				if err != nil {
					return w.None{}, nil
				}
				if err := conn.WriteMessage(mt, msg); err != nil {
					return w.None{}, err
				}
			}
		}))

	// 聊天广播 Hub：共享状态 + 连接生命周期管理。
	hub := newChatHub()
	app.Must(w.Handle(w.Get("/ws/chat"),
		w.WSConn(),
		w.Upgraded[w.None](),
		func(conn *websocket.Conn) (w.None, error) {
			hub.join(conn)
			defer hub.leave(conn)
			for {
				mt, msg, err := conn.ReadMessage()
				if err != nil {
					return w.None{}, nil
				}
				hub.broadcast(mt, msg)
			}
		}))

	// 自定义升级器：生产环境的跨域/子协议策略。
	strictWS := w.UpgradeWS(&websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return r.Host == "localhost:8092" },
	})
	app.Must(w.Handle(w.Get("/ws/strict"), strictWS, w.Upgraded[w.None](),
		func(conn *websocket.Conn) (w.None, error) {
			defer conn.Close()
			return w.None{}, nil
		}))

	// ── 静态文件与 SPA ───────────────────────────────────────────
	app.Must(w.Static("/assets/", "./examples/tasks/public/assets"))
	app.Must(w.SPA("/app/", "./examples/tasks/public/spa")...)

	// ── 资源 CRUD：组前缀 + 鉴权 + 约束 + MapIn 组合 ─────────────
	api := app.Group("/tasks", requireAuth)

	type listIn struct {
		page, size int
		status     string
	}
	listDesc := w.InFunc(func(r w.Req) (listIn, error) {
		page := r.Query().IntDefault("page", 1)
		if page < 1 {
			return listIn{}, httperr.BadRequest(errors.New("page must be >= 1"))
		}
		size := r.Query().IntDefault("size", 20)
		if size < 1 || size > 100 {
			return listIn{}, httperr.BadRequest(errors.New("size must be 1..100"))
		}
		status := r.Query().String("status")
		if status != "" && status != "open" && status != "done" {
			return listIn{}, httperr.BadRequest(errors.New("status must be open or done"))
		}
		return listIn{page: page, size: size, status: status}, nil
	})
	api.Must(w.Handle(w.Get(""), listDesc, w.JSON[taskList](),
		func(in listIn) (taskList, error) {
			return db.list(in.page, in.size, in.status), nil
		}).Doc(w.Summary("分页列出任务"), w.Tags("tasks")))

	api.Must(w.CreatedJSON("", w.BodyJSON[createTask](func(c createTask) error {
		if c.Title == "" {
			return errors.New("title is required")
		}
		return nil
	}), func(c createTask) (task, error) { return db.create(c.Title), nil }))

	api.Must(w.PutJSON("/{id}",
		w.MapIn(w.PathInt64("id"), w.BodyJSON[updateTask](),
			func(id int64, body updateTask) updateInput { return updateInput{ID: id, Body: body} }),
		func(in updateInput) (task, error) { return db.update(in.ID, in.Body) }))

	api.Must(w.GetJSON("/{id}", w.PathInt64("id"), func(id int64) (task, error) {
		t, ok := db.get(id)
		if !ok {
			return task{}, httperr.NotFound().With("id", id)
		}
		return t, nil
	}))
	api.Must(w.PostJSON("/{id}/done", w.PathInt64("id"), func(id int64) (task, error) {
		return db.done(id)
	}))
	api.Must(w.Handle(w.Delete("/{id}"), w.PathInt64("id"), w.NoContent[w.None](),
		func(id int64) (w.None, error) { return w.None{}, db.remove(id) }))

	whoDesc := w.InFunc(func(r w.Req) (string, error) {
		u, ok := userKey.Get(r.Raw())
		if !ok {
			return "", httperr.Unauthorized()
		}
		return u, nil
	})
	app.Must(w.Handle(w.Get("/whoami"), whoDesc, w.Text(),
		func(u string) (string, error) { return u, nil }).With(requireAuth))

	return app
}

func main() {
	app := newApp(&store{tasks: map[int64]task{}})

	srv := &http.Server{Addr: ":8092", Handler: app}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Println("tasks API listening on :8092")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
