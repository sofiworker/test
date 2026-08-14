package web_test

import (
	"encoding/json"
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
		`"id"`,         // user struct property
		`"name"`,       // user struct property
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
