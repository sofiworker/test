// demo：端点即数据 + 注册层糖 + 输入组合的完整示范。
// 跑起来：go run ./demo
package main

import (
	"log"
	"net/http"

	w "example.com/web"
	"example.com/web/httperr"
)

type user struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

var users = map[int64]user{
	1: {ID: 1, Name: "Ada"},
}

func main() {
	app := w.New()
	app.Use(w.Logger(log.Default()))

	// ── 温和版：输出即入口，无输入用 NoIn + None ─────────────────
	app.Must(
		w.GetJSON("/v1/users/{id}", w.PathInt64("id"), func(id int64) (*user, error) {
			u, ok := users[id]
			if !ok {
				return nil, httperr.NotFound().With("id", id)
			}
			return &u, nil
		}),
		w.GetText("/v1/hello", w.NoIn(), func(w.None) (string, error) {
			return "hello, world", nil
		}),
	)

	// ── 彻底版：0 变体，连 None 都不用写 ─────────────────────────
	app.Must(
		w.GetText0("/v2/hello", func() (string, error) { return "hello, world", nil }),
		w.CreatedJSON("/v2/users", w.BodyJSON[user](), func(u user) (user, error) {
			users[u.ID] = u
			return u, nil
		}),
	)

	// ── 输入组合：path + body 一行拼装（PUT 更新资源的标准形态）─────
	app.Must(w.PutJSON("/v2/users/{id}",
		w.All(w.PathInt64("id"), w.BodyJSON[user]()),
		func(p w.Pair[int64, user]) (user, error) {
			p.Second.ID = p.First
			users[p.First] = p.Second
			return p.Second, nil
		}))

	// ── Group：前缀 + 组中间件，一组路由一次挂载 ─────────────────
	g := app.Group("/api", func(next w.Handler) w.Handler {
		return func(c *w.Ctx) error {
			c.Header().Set("X-Api", "v1")
			return next(c)
		}
	})
	g.Must(
		w.GetJSON("/users/{id}", w.PathInt64("id"), func(id int64) (*user, error) {
			u, ok := users[id]
			if !ok {
				return nil, httperr.NotFound()
			}
			return &u, nil
		}),
	)

	log.Println("listening on :8091")
	if err := http.ListenAndServe(":8091", app); err != nil {
		log.Fatal(err)
	}
}
