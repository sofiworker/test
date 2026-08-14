package web

import (
	"strings"
	"testing"
)

func h() Handler { return func(*Ctx) error { return nil } }

func insertRoutes(t *testing.T, n *node, routes []struct{ method, path string }) {
	t.Helper()
	for _, r := range routes {
		segs, err := parsePath(r.path)
		if err != nil {
			t.Fatalf("parsePath(%q): %v", r.path, err)
		}
		if err := n.insert(segs, h(), r.method); err != nil {
			t.Fatalf("insert %s %s: %v", r.method, r.path, err)
		}
	}
}

func mustParams(t *testing.T, ps []param, want ...param) {
	t.Helper()
	if len(ps) != len(want) {
		t.Fatalf("params = %v, want %v", ps, want)
	}
	for i := range want {
		if ps[i] != want[i] {
			t.Fatalf("params[%d] = %v, want %v", i, ps[i], want[i])
		}
	}
}

func TestMatch(t *testing.T) {
	n := &node{}
	insertRoutes(t, n, []struct{ method, path string }{
		{"GET", "/"},
		{"GET", "/user"},
		{"GET", "/users"},
		{"GET", "/users/new"},
		{"GET", "/users/{id}"},
		{"GET", "/users/{id}/posts"},
		{"GET", "/users/{id}/posts/{pid}"},
		{"GET", "/files/{path...}"},
		{"GET", "/api/{v}/health"},
		{"POST", "/users"},
		{"GET", "/usersettings"},
	})

	cases := []struct {
		path   string
		method string // "" means: only check that the path matches at all
		want   []param
	}{
		{"/", "GET", nil},
		{"/user", "GET", nil},
		{"/users", "GET", nil},
		{"/users", "POST", nil},
		{"/users/new", "GET", nil}, // static beats {id}
		{"/users/42", "GET", []param{{"id", "42"}}},
		{"/users/42/posts", "GET", []param{{"id", "42"}}},
		{"/users/42/posts/7", "GET", []param{{"id", "42"}, {"pid", "7"}}},
		{"/usersettings", "GET", nil}, // radix split, must not hit /user
		{"/files/a/b/c.txt", "GET", []param{{"path", "a/b/c.txt"}}},
		{"/files/a", "GET", []param{{"path", "a"}}},
		{"/api/v2/health", "GET", []param{{"v", "v2"}}},
	}
	for _, tc := range cases {
		nd, ps, ok := n.match("", tc.path, 1, nil)
		if !ok {
			t.Errorf("match(%q): not found, want match", tc.path)
			continue
		}
		if tc.method != "" && nd.handlerFor(tc.method) == nil {
			t.Errorf("match(%q): no %s handler, want one", tc.path, tc.method)
			continue
		}
		mustParams(t, ps, tc.want...)
	}

	for _, p := range []string{"/use", "/nope", "/users/42/nope", "/files", "/api/health"} {
		nd, _, ok := n.match("", p, 1, nil)
		if ok && len(nd.leafHandlers) > 0 {
			t.Errorf("match(%q): unexpectedly matched", p)
		}
	}

	// 405-style: path exists, method does not.
	nd, _, ok := n.match("", "/users/42", 1, nil)
	if !ok || nd.handlerFor("DELETE") != nil {
		t.Fatalf("match(/users/42): want node without DELETE handler")
	}
}

func TestParamVsStaticPrefixSplit(t *testing.T) {
	n := &node{}
	insertRoutes(t, n, []struct{ method, path string }{
		{"GET", "/us/{x}"},
		{"GET", "/users"},
	})
	// /us/123 matches the param route.
	nd, ps, ok := n.match("", "/us/123", 1, nil)
	if !ok || nd.handlerFor("GET") == nil {
		t.Fatalf("match(/us/123): want param route")
	}
	mustParams(t, ps, param{"x", "123"})
	// /users matches the static route.
	nd, _, ok = n.match("", "/users", 1, nil)
	if !ok || nd.handlerFor("GET") == nil {
		t.Fatalf("match(/users): want static route")
	}
	// /users/123 must NOT fall through to the {x} param: "users" is not a
	// complete static match of "us", and params only match whole segments.
	nd, _, ok = n.match("", "/users/123", 1, nil)
	if ok && len(nd.leafHandlers) > 0 {
		t.Fatalf("match(/users/123): must not match /us/{x}")
	}
}

func TestRegistrationErrors(t *testing.T) {
	cases := []struct {
		name   string
		routes []struct{ method, path string }
		want   string // substring of the error
	}{
		{
			"conflicting param names",
			[]struct{ method, path string }{{"GET", "/a/{x}/b"}, {"GET", "/a/{y}/b"}},
			"conflicting parameter names",
		},
		{
			"catch-all not last",
			[]struct{ method, path string }{{"GET", "/a/{x...}/b"}},
			"must be the last segment",
		},
		{
			"duplicate route",
			[]struct{ method, path string }{{"GET", "/a"}, {"GET", "/a"}},
			"duplicate route",
		},
		{
			"wildcard must be whole segment",
			[]struct{ method, path string }{{"GET", "/a/x}y"}},
			"whole path segment",
		},
		{
			"empty wildcard name",
			[]struct{ method, path string }{{"GET", "/a/{}"}},
			"must not be empty",
		},
		{
			"malformed wildcard",
			[]struct{ method, path string }{{"GET", "/a/{x"}},
			"malformed wildcard",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &node{}
			for i, r := range tc.routes {
				segs, err := parsePath(r.path)
				if err == nil {
					err = n.insert(segs, h(), r.method)
				}
				if i < len(tc.routes)-1 {
					// Earlier routes in a conflict case must register cleanly.
					if err != nil {
						t.Fatalf("insert %s %s: unexpected error: %v", r.method, r.path, err)
					}
					continue
				}
				if err == nil {
					t.Fatalf("insert %s %s: expected error containing %q", r.method, r.path, tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("insert %s %s: error %q does not contain %q", r.method, r.path, err, tc.want)
				}
			}
		})
	}
}

func TestParsePathErrors(t *testing.T) {
	for _, p := range []string{"", "users", "/users/", "/a//b", "/a/", "//"} {
		if _, err := parsePath(p); err == nil {
			t.Errorf("parsePath(%q): expected error", p)
		}
	}
	if segs, err := parsePath("/"); err != nil || len(segs) != 0 {
		t.Errorf("parsePath(/): segs=%v err=%v", segs, err)
	}
}

func TestSameNameParamMerges(t *testing.T) {
	n := &node{}
	insertRoutes(t, n, []struct{ method, path string }{
		{"GET", "/a/{x}/b"},
		{"GET", "/a/{x}/c"},
	})
	for _, p := range []string{"/a/1/b", "/a/2/c"} {
		nd, ps, ok := n.match("", p, 1, nil)
		if !ok || nd.handlerFor("GET") == nil {
			t.Fatalf("match(%q): want match", p)
		}
		mustParams(t, ps, param{"x", p[3:4]})
	}
}

func TestTrailingSlashRequestMatchesRoute(t *testing.T) {
	n := &node{}
	insertRoutes(t, n, []struct{ method, path string }{{"GET", "/users"}})
	nd, _, ok := n.match("", "/users/", 1, nil)
	if !ok || nd.handlerFor("GET") == nil {
		t.Fatalf("match(/users/): want the /users node (trailing slash on request is tolerated)")
	}
}
