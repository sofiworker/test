package web_test

import (
	"strings"
	"testing"

	web "example.com/web"
	"example.com/web/httperr"
)

func TestRouteDocAttachment(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/users/{id}"), web.PathInt64("id"), web.JSON[*user](),
		func(id int64) (*user, error) {
			if id == 99 {
				return nil, httperr.NotFound()
			}
			return &user{ID: id}, nil
		}).Doc(web.Summary("获取用户"), web.Tags("users"), web.Deprecated()))

	if rec := do(t, app, "GET", "/users/7"); rec.Code != 200 || body(t, rec) != `{"id":7,"name":""}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
	if rec := do(t, app, "GET", "/users/99"); rec.Code != 404 {
		t.Fatalf("404: %d", rec.Code)
	}
}

func TestRouteDocMiddlewareChain(t *testing.T) {
	app := web.New()
	var events []string
	mw := func(next web.Handler) web.Handler {
		return func(c *web.Ctx) error {
			events = append(events, "mw")
			return next(c)
		}
	}
	r := web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "ok", nil }).
		With(mw).Doc(web.Summary("x"))
	if r == nil {
		t.Fatal("nil route")
	}
	app.Must(r)
	do(t, app, "GET", "/x")
	if len(events) != 1 || events[0] != "mw" {
		t.Fatalf("middleware: %v", events)
	}
}

func TestRouteDocAutoInference(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/users/{id}"), web.PathInt64("id"), web.JSON[*user](),
		func(id int64) (*user, error) { return &user{ID: id}, nil }))
	// 用户什么文档都没写：summary 与 operationId 自动反推
	doc := app.Doc(web.Info{Title: "t", Version: "1"})
	op := doc.Paths["/users/{id}"]["get"]
	if op == nil {
		t.Fatalf("no op")
	}
	if op.Summary != "GET /users/{id}" {
		t.Fatalf("auto summary = %q", op.Summary)
	}
	if op.OperationID != "get_users_{id}" {
		t.Fatalf("auto operationId = %q", op.OperationID)
	}

	// 用户写了的字段优先
	app2 := web.New()
	app2.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "", nil }).
		Doc(web.Summary("自定义"), web.Description("详情"), web.OperationID("customOp")))
	op2 := app2.Doc(web.Info{Title: "t", Version: "1"}).Paths["/x"]["get"]
	if op2.Summary != "自定义" || op2.OperationID != "customOp" || op2.Description != "详情" {
		t.Fatalf("declared docs not honored: %+v", op2)
	}
}

func TestDuplicateRouteError(t *testing.T) {
	app := web.New()
	app.Must(web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "ok", nil }))
	err := app.Mount(web.Handle(web.Get("/x"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "dup", nil }))
	if err == nil || !strings.Contains(err.Error(), "duplicate route") {
		t.Fatalf("conflict: %v", err)
	}
}
