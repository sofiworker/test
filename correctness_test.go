package web_test

import (
	"errors"
	"html/template"
	"testing"

	web "example.com/web"
)

// badData 的 Boom 返回错误：模板执行必然失败。
type badData struct{}

func (badData) Boom() (string, error) { return "", errors.New("boom") }

// 问题 1 回归：共享响应头切片污染（外部复现的案例）。
func TestHeaderSliceIsolation(t *testing.T) {
	app := web.New()
	first := true
	app.Use(func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			err := next(c)
			if first {
				first = false
				if v := c.Header()["Content-Type"]; len(v) > 0 {
					v[0] = "polluted"
				}
			}
			return err
		}
	})
	app.Must(web.GetJSON("/x", web.NoIn(),
		func(web.None) (map[string]string, error) { return map[string]string{"k": "v"}, nil }))

	// 第一个请求触发原地修改
	do(t, app, "GET", "/x")
	// 第二个请求必须拿到干净的 Content-Type
	rec := do(t, app, "GET", "/x")
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("header polluted across requests: %q", ct)
	}
}

// 问题 2 回归：序列化错误必须走 500 错误管道，而不是 200 空响应。
func TestSerializationErrorSemantics(t *testing.T) {
	type unsupported struct {
		Ch chan int `json:"ch"`
	}
	app := web.New()
	// 通用路径：Handle + JSON 渲染器
	app.Must(web.Handle(web.Get("/generic"), web.NoIn(), web.JSON[unsupported](),
		func(web.None) (unsupported, error) { return unsupported{Ch: make(chan int)}, nil }))
	// 糖入口：GetJSON
	app.Must(web.GetJSON("/sugar", web.NoIn(),
		func(web.None) (unsupported, error) { return unsupported{Ch: make(chan int)}, nil }))
	// XML 同样
	app.Must(web.Handle(web.Get("/xml"), web.NoIn(), web.XML[unsupported](),
		func(web.None) (unsupported, error) { return unsupported{}, nil }))
	// 模板执行错误同样：error-返回方法必然让 Execute 失败
	type badData struct{}
	badTmpl := template.Must(template.New("bad").Parse("{{.Boom}}"))
	app.Must(web.Handle(web.Get("/html"), web.NoIn(), web.HTML[badData](badTmpl, "bad"),
		func(web.None) (badData, error) { return badData{}, nil }))

	for _, p := range []string{"/generic", "/sugar", "/xml", "/html"} {
		rec := do(t, app, "GET", p)
		if rec.Code != 500 || body(t, rec) != `{"error":"internal server error"}` {
			t.Fatalf("%s: %d %q, want 500 JSON error", p, rec.Code, body(t, rec))
		}
	}
}
