// tasks 是框架的完整参考应用：多资源 CRUD + Bearer 鉴权 + 分页/过滤 +
// 状态机 + 显式校验 + OpenAPI + 优雅停机。跑起来：
//
//	go run ./examples/tasks
//	curl localhost:8092/tasks?page=1&size=10
//	curl -X POST -H 'Authorization: Bearer dev-token' -d '{"title":"ship it"}' localhost:8092/tasks
//	curl localhost:8092/openapi.json
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	w "example.com/web"
	"example.com/web/httperr"
)

type task struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // open | done
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

// store 是内存实现；handler 经闭包捕获依赖（Go 惯例）。
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

func newApp(db *store) *w.App {
	app := w.New()
	app.Use(w.RequestID(w.NewKey[string]("request-id"), w.NewID), w.Recover(), w.Timeout(5*time.Second), w.Logger(log.Default()))
	app.UseCORS(w.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE"},
		AllowHeaders: []string{"Authorization", "Content-Type"},
	})

	app.Must(w.GetText0("/healthz", func() (string, error) { return "ok", nil }))

	// 公开：OpenAPI 文档即路由
	app.Must(w.GetJSON0("/openapi.json", func() (w.OpenAPIDoc, error) {
		return app.Doc(w.Info{Title: "tasks API", Version: "1.0.0"}), nil
	}))

	api := app.Group("/tasks", requireAuth)

	// 分页 + 过滤：约束挂在描述器上，状态枚举在 handler 显式校验
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
	// 组内根路由用空路径：组前缀拼接后才成为 "/tasks"
	api.Must(w.GetJSON("", listDesc, func(in listIn) (taskList, error) {
		return db.list(in.page, in.size, in.status), nil
	}))

	api.Must(w.CreatedJSON("", w.BodyJSON[createTask](func(c createTask) error {
		if c.Title == "" {
			return errors.New("title is required")
		}
		return nil
	}), func(c createTask) (task, error) { return db.create(c.Title), nil }))

	// path + body 组合更新（标准 REST 形态）
	api.Must(w.PutJSON("/{id}",
		w.All(w.PathInt64("id"), w.BodyJSON[updateTask]()),
		func(p w.Pair[int64, updateTask]) (task, error) { return db.update(p.First, p.Second) }))

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

	// 类型键端到端：中间件写入 → InFunc 读出 → handler 契约
	whoDesc := w.InFunc(func(r w.Req) (string, error) {
		u, ok := userKey.Get(r.Raw())
		if !ok {
			return "", httperr.Unauthorized()
		}
		return u, nil
	})
	// 路由级中间件（.With）：whoami 不在 /tasks 组内，单独挂鉴权
	app.Must(w.GetText("/whoami", whoDesc, func(u string) (string, error) { return u, nil }).
		With(requireAuth))

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
