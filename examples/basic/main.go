// Command basic demonstrates the endpoint-as-data design: routes are
// first-class values, handlers are plain functions over declared contracts.
// Run it and try:
//
//	curl localhost:8090/
//	curl localhost:8090/users/1
//	curl localhost:8090/users/abc   # 400
//	curl localhost:8090/users/9     # 404
//	curl -X POST -d '{"id":3,"name":"Lin"}' localhost:8090/users
package main

import (
	"log"
	"net/http"

	web "example.com/web"
	"example.com/web/httperr"
)

type user struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

var users = map[int64]user{
	1: {ID: 1, Name: "Ada"},
	2: {ID: 2, Name: "Grace"},
}

func main() {
	app := web.New()
	app.Use(web.Recover(), web.Logger(log.Default()))

	app.Must(web.Handle(
		web.Get("/"),
		web.NoIn(),
		web.Text(),
		func(web.None) (string, error) { return "hello, world", nil },
	))

	app.Must(web.Handle(
		web.Get("/users/{id}"),
		web.PathInt64("id"), // 输入契约：{id} 解析为 int64，失败自动 400
		web.JSON[*user](),   // 输出契约：JSON
		func(id int64) (*user, error) {
			u, ok := users[id]
			if !ok {
				return nil, httperr.NotFound().With("id", id)
			}
			return &u, nil
		},
	))

	app.Must(web.Handle(
		web.Post("/users"),
		web.BodyJSON[user](), // 输入契约：请求体 → user
		web.Status(201, web.JSON[user]()),
		func(u user) (user, error) {
			users[u.ID] = u
			return u, nil
		},
	))

	log.Println("listening on :8090")
	if err := http.ListenAndServe(":8090", app); err != nil {
		log.Fatal(err)
	}
}
