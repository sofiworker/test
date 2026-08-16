package web

import (
	"net/http"
	"net/url"
	"testing"
)

// 模糊测试：路由解析/匹配与提取器在任意输入下不得 panic——
// 生产可用性的第一道防线。运行：
//
//	go test -fuzz=FuzzRouterNoPanic -fuzztime=30s
//	go test -fuzz=FuzzExtractorNoPanic -fuzztime=30s

func FuzzRouterNoPanic(f *testing.F) {
	for _, seed := range []string{
		"/", "/a", "/a/b", "/users/{id}", "/a/{b...}", "/a//b", "a", "/a/",
		"/{x}/{y}/z", "/中文/路径", "/a/{x}/b/{y}", "//", "/{", "/}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic on path %q: %v", path, r)
			}
		}()
		segs, err := parsePath(path)
		if err != nil {
			return
		}
		n := &node{}
		if err := n.insert(segs, func(*Ctx) error { return nil }, "GET"); err != nil {
			return
		}
		pos := 0
		if len(path) > 0 && path[0] == '/' {
			pos = 1
		}
		n.match(path, pos, nil)
	})
}

func FuzzRouterRandomTableNoPanic(f *testing.F) {
	for _, seed := range []string{"/a", "/b/{x}", "/a/b/c", "/{x}/{y}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, r1 string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic building/matching route %q: %v", r1, r)
			}
		}()
		segs, err := parsePath(r1)
		if err != nil {
			return
		}
		n := &node{}
		if err := n.insert(segs, func(*Ctx) error { return nil }, "GET"); err != nil {
			return
		}
		// 随机路径去撞这张表
		n.match(r1, posOf(r1), nil)
		n.match(r1+"/extra", posOf(r1+"/extra"), nil)
	})
}

func posOf(p string) int {
	if len(p) > 0 && p[0] == '/' {
		return 1
	}
	return 0
}

func FuzzExtractorNoPanic(f *testing.F) {
	for _, seed := range []string{"", "123", "-5", "0x10", "true", "1e3", "abc", " 42", "4.2"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic extracting %q: %v", s, r)
			}
		}()
		c := &Ctx{
			params: []param{{key: "x", value: s}},
			Req:    &http.Request{URL: &url.URL{RawQuery: "x=" + url.QueryEscape(s)}},
		}
		_ = c.Param("x")
		_, _ = PathAccessor{c: c}.Int64("x")
		_, _ = PathAccessor{c: c}.Bool("x")
		_, _ = PathAccessor{c: c}.Float64("x")
		_ = QueryAccessor{c: c}.String("x")
		_, _ = QueryAccessor{c: c}.Int("x")
	})
}
