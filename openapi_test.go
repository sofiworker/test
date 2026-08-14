package web_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	web "example.com/web"
)

func TestOpenAPIGeneration(t *testing.T) {
	app := web.New()
	app.Must(
		web.GetJSON("/users/{id}", web.PathInt64("id"),
			func(id int64) (*user, error) { return &user{ID: id}, nil }),
		web.PostJSON("/users", web.BodyJSON[user](),
			func(u user) (user, error) { return u, nil }),
		web.GetText("/hello", web.NoIn(),
			func(web.None) (string, error) { return "hi", nil }),
		web.PutJSON("/users/{id}", web.All(web.PathInt64("id"), web.BodyJSON[user]()),
			func(p web.Pair[int64, user]) (user, error) { return p.Second, nil }),
	)

	doc := app.Doc(web.Info{Title: "demo", Version: "1.0.0"})
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)

	checks := []string{
		`"openapi": "3.0.3"`,
		`"title": "demo"`,
		`"/users/{id}"`,
		`"in": "path"`,
		`"format": "int64"`,
		`"requestBody"`,
		`"properties"`,
		`"id"`,                        // user struct property
		`"name"`,                      // user struct property
		`"text/plain; charset=utf-8"`, // text renderer content type
		`"application/json"`,
		`"400"`,
		`"500"`,
	}
	for _, want := range checks {
		if !strings.Contains(s, want) {
			t.Errorf("OpenAPI doc missing %q\n%s", want, s)
		}
	}

	// The composed route carries BOTH the path parameter and the body.
	put := doc.Paths["/users/{id}"]["put"]
	if put == nil || len(put.Parameters) != 1 || put.RequestBody == nil {
		t.Fatalf("PUT route metadata incomplete: %+v", put)
	}

	// The 404 default is always present.
	get := doc.Paths["/users/{id}"]["get"]
	if get == nil || get.Responses["404"] == nil {
		t.Fatalf("GET route missing 404 response: %+v", get)
	}
}

func TestOpenAPIServedRoute(t *testing.T) {
	app := web.New()
	app.Must(web.GetJSON("/users/{id}", web.PathInt64("id"),
		func(id int64) (*user, error) { return &user{ID: id}, nil }))
	app.Must(web.GetJSON0("/openapi.json",
		func() (web.OpenAPIDoc, error) {
			return app.Doc(web.Info{Title: "api", Version: "0.1.0"}), nil
		}))

	rec := do(t, app, "GET", "/openapi.json")
	if rec.Code != 200 || !strings.Contains(body(t, rec), `"/users/{id}"`) {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
}

func TestConstraintsValidationAndSchema(t *testing.T) {
	app := web.New()
	app.Must(
		web.GetJSON("/items/{id}", web.PathInt64("id", web.Min(1), web.Max(99)),
			func(id int64) (*user, error) { return &user{ID: id}, nil }),
		web.GetJSON("/search", web.QueryString("sort", web.Enum("asc", "desc")),
			func(s string) (*user, error) { return &user{Name: s}, nil }),
	)

	// 运行时校验
	if rec := do(t, app, "GET", "/items/5"); rec.Code != 200 {
		t.Fatalf("valid: %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/items/0"); rec.Code != 400 {
		t.Fatalf("below min: %d, want 400", rec.Code)
	}
	if rec := do(t, app, "GET", "/items/100"); rec.Code != 400 {
		t.Fatalf("above max: %d, want 400", rec.Code)
	}
	if rec := do(t, app, "GET", "/search?sort=asc"); rec.Code != 200 {
		t.Fatalf("valid enum: %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/search?sort=sideways"); rec.Code != 400 {
		t.Fatalf("bad enum: %d, want 400", rec.Code)
	}

	// OpenAPI 同步
	doc := app.Doc(web.Info{Title: "c", Version: "1"})
	b, _ := json.Marshal(doc)
	s := string(b)
	for _, want := range []string{`"minimum":1`, `"maximum":99`, `"enum":["asc","desc"]`} {
		if !strings.Contains(s, want) {
			t.Errorf("schema missing %s", want)
		}
	}
}

func TestHeaderStringAndInFuncMeta(t *testing.T) {
	app := web.New()
	app.Must(web.GetJSON("/h", web.HeaderString("X-Tenant"),
		func(tenant string) (*user, error) { return &user{Name: tenant}, nil }))

	req := httptest.NewRequest("GET", "/h", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 200 || body(t, rec) != `{"id":0,"name":"acme"}` {
		t.Fatalf("%d %q", rec.Code, body(t, rec))
	}
	if rec := do(t, app, "GET", "/h"); rec.Code != 400 {
		t.Fatalf("missing header: %d, want 400", rec.Code)
	}

	// 自定义描述器自报元数据
	type in struct{ q string }
	desc := web.InFuncMeta(func(r web.Req) (in, error) {
		return in{q: r.Query().String("q")}, nil
	}, web.OpMeta{Parameters: []*web.Parameter{{
		Name: "q", In: "query",
		Schema: &web.Schema{Type: "string"},
	}}})
	app.Must(web.GetJSON("/custom", desc,
		func(v in) (map[string]string, error) { return map[string]string{"q": v.q}, nil }))
	doc := app.Doc(web.Info{Title: "c", Version: "1"})
	custom := doc.Paths["/custom"]["get"]
	if custom == nil || len(custom.Parameters) != 1 || custom.Parameters[0].Name != "q" {
		t.Fatalf("custom descriptor metadata missing: %+v", custom)
	}
	if hdr := doc.Paths["/h"]["get"]; hdr == nil || len(hdr.Parameters) != 1 || hdr.Parameters[0].In != "header" {
		t.Fatalf("header metadata missing: %+v", hdr)
	}
}
