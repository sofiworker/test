package benchmarks

// 同一批端点（examples/tasks 的语义）在 web 与 gin 下的对照基准：
// 各自的惯用写法、相同的校验、相同的状态码与 JSON 形态、相同的 store。
// 双方默认 JSON 引擎：web = goccy/go-json，gin = encoding/json。

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	web "example.com/web"
	"example.com/web/httperr"
)

type task struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
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

type benchStore struct {
	next  int64
	tasks map[int64]task
}

func newBenchStore(n int) *benchStore {
	s := &benchStore{tasks: map[int64]task{}}
	for i := 0; i < n; i++ {
		s.next++
		s.tasks[s.next] = task{ID: s.next, Title: "task", Status: "open"}
	}
	return s
}

func (s *benchStore) create(title string) task {
	s.next++
	t := task{ID: s.next, Title: title, Status: "open"}
	s.tasks[t.ID] = t
	return t
}

func (s *benchStore) get(id int64) (task, bool) {
	t, ok := s.tasks[id]
	return t, ok
}

func (s *benchStore) update(id int64, u updateTask) (task, bool) {
	t, ok := s.tasks[id]
	if !ok {
		return task{}, false
	}
	if u.Title != "" {
		t.Title = u.Title
	}
	if u.Status == "open" || u.Status == "done" {
		t.Status = u.Status
	}
	s.tasks[id] = t
	return t, true
}

func (s *benchStore) done(id int64) (task, bool) {
	t, ok := s.tasks[id]
	if !ok || t.Status == "done" {
		return task{}, false
	}
	t.Status = "done"
	s.tasks[id] = t
	return t, true
}

func (s *benchStore) list(page, size int, status string) taskList {
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

// ---- web 实现（examples/tasks 的语义）----

func newWebTasks() *web.App {
	db := newBenchStore(20)
	app := web.New()

	requireAuth := func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			if strings.TrimPrefix(c.Req.Header.Get("Authorization"), "Bearer ") != "dev-token" {
				return httperr.Unauthorized()
			}
			return next(c)
		}
	}

	api := app.Group("/tasks", requireAuth)

	type listIn struct {
		page, size int
		status     string
	}
	listDesc := web.InFunc(func(r web.Req) (listIn, error) {
		page := r.Query().IntDefault("page", 1)
		size := r.Query().IntDefault("size", 20)
		status := r.Query().String("status")
		if page < 1 || size < 1 || size > 100 || (status != "" && status != "open" && status != "done") {
			return listIn{}, httperr.BadRequest(strconv.ErrSyntax)
		}
		return listIn{page: page, size: size, status: status}, nil
	})
	api.Must(web.GetJSON("", listDesc, func(in listIn) (taskList, error) {
		return db.list(in.page, in.size, in.status), nil
	}))

	api.Must(web.CreatedJSON("", web.BodyJSON[createTask](func(c createTask) error {
		if c.Title == "" {
			return httperr.BadRequest(strconv.ErrSyntax)
		}
		return nil
	}), func(c createTask) (task, error) { return db.create(c.Title), nil }))

	api.Must(web.PutJSON("/{id}", web.All(web.PathInt64("id"), web.BodyJSON[updateTask]()),
		func(p web.Pair[int64, updateTask]) (task, error) {
			t, ok := db.update(p.First, p.Second)
			if !ok {
				return task{}, httperr.NotFound()
			}
			return t, nil
		}))

	api.Must(web.GetJSON("/{id}", web.PathInt64("id"), func(id int64) (task, error) {
		t, ok := db.get(id)
		if !ok {
			return task{}, httperr.NotFound()
		}
		return t, nil
	}))

	api.Must(web.PostJSON("/{id}/done", web.PathInt64("id"), func(id int64) (task, error) {
		t, ok := db.done(id)
		if !ok {
			return task{}, httperr.Conflict("task already done")
		}
		return t, nil
	}))

	return app
}

// ---- gin 实现（惯用写法）----

func newGinTasks() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	db := newBenchStore(20)
	r := gin.New()

	auth := func(c *gin.Context) {
		if strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ") != "dev-token" {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
	}

	g := r.Group("/tasks", auth)

	g.GET("", func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
		status := c.Query("status")
		if page < 1 || size < 1 || size > 100 || (status != "" && status != "open" && status != "done") {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		c.JSON(200, db.list(page, size, status))
	})

	g.POST("", func(c *gin.Context) {
		var in createTask
		if err := c.ShouldBindJSON(&in); err != nil || in.Title == "" {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		c.JSON(201, db.create(in.Title))
	})

	g.PUT("/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		var in updateTask
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		t, ok := db.update(id, in)
		if !ok {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.JSON(200, t)
	})

	g.GET("/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		t, ok := db.get(id)
		if !ok {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.JSON(200, t)
	})

	g.POST("/:id/done", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.JSON(400, gin.H{"error": "bad request"})
			return
		}
		t, ok := db.done(id)
		if !ok {
			c.JSON(409, gin.H{"error": "task already done"})
			return
		}
		c.JSON(200, t)
	})

	return r
}

// ---- 基准 ----

func benchTasks(b *testing.B, h http.Handler, method, path string, body []byte, auth bool) {
	w := &nullWriter{}
	var req *http.Request
	if body != nil {
		rd := bytes.NewReader(body)
		req = httptest.NewRequest(method, path, rd)
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer dev-token")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if body != nil {
			rd := bytes.NewReader(body)
			req.Body = io.NopCloser(rd)
		}
		h.ServeHTTP(w, req)
	}
}

func BenchmarkWebTasksList(b *testing.B) {
	benchTasks(b, newWebTasks(), "GET", "/tasks?page=1&size=20", nil, true)
}
func BenchmarkGinTasksList(b *testing.B) {
	benchTasks(b, newGinTasks(), "GET", "/tasks?page=1&size=20", nil, true)
}
func BenchmarkWebTasksGet(b *testing.B) { benchTasks(b, newWebTasks(), "GET", "/tasks/1", nil, true) }
func BenchmarkGinTasksGet(b *testing.B) { benchTasks(b, newGinTasks(), "GET", "/tasks/1", nil, true) }
func BenchmarkWebTasksNotFound(b *testing.B) {
	benchTasks(b, newWebTasks(), "GET", "/tasks/9999", nil, true)
}
func BenchmarkGinTasksNotFound(b *testing.B) {
	benchTasks(b, newGinTasks(), "GET", "/tasks/9999", nil, true)
}
func BenchmarkWebTasksCreate(b *testing.B) {
	benchTasks(b, newWebTasks(), "POST", "/tasks", []byte(`{"title":"bench"}`), true)
}
func BenchmarkGinTasksCreate(b *testing.B) {
	benchTasks(b, newGinTasks(), "POST", "/tasks", []byte(`{"title":"bench"}`), true)
}
func BenchmarkWebTasksUpdate(b *testing.B) {
	benchTasks(b, newWebTasks(), "PUT", "/tasks/1", []byte(`{"title":"bench"}`), true)
}
func BenchmarkGinTasksUpdate(b *testing.B) {
	benchTasks(b, newGinTasks(), "PUT", "/tasks/1", []byte(`{"title":"bench"}`), true)
}
func BenchmarkWebTasksDone(b *testing.B) {
	benchTasks(b, newWebTasks(), "POST", "/tasks/2/done", nil, true)
}
func BenchmarkGinTasksDone(b *testing.B) {
	benchTasks(b, newGinTasks(), "POST", "/tasks/2/done", nil, true)
}
func BenchmarkWebTasksAuthFail(b *testing.B) {
	benchTasks(b, newWebTasks(), "GET", "/tasks", nil, false)
}
func BenchmarkGinTasksAuthFail(b *testing.B) {
	benchTasks(b, newGinTasks(), "GET", "/tasks", nil, false)
}
