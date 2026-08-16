package benchmarks

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	web "example.com/web"
)

// 超大规模压力：N 个资源 × 6 种路由形态（静态 GET、参数 GET、POST、PUT、
// DELETE、通配符）。N=20000 时共 12 万条路由。

const stressN = 20000

func newWebStress(n int) *web.App {
	app := web.New()
	for i := 0; i < n; i++ {
		base := fmt.Sprintf("/api/v%d/res%d", i, i)
		app.Must(web.Handle(web.Get(base), web.NoIn(), web.Text(),
			func(web.None) (string, error) { return "ok", nil }))
		app.Must(web.GetText(base+"/{id}", web.PathString("id"),
			func(id string) (string, error) { return id, nil }))
		app.Must(web.Handle(web.Post(base), web.NoIn(), web.Text(),
			func(web.None) (string, error) { return "ok", nil }))
		app.Must(web.PutText(base+"/{id}", web.PathString("id"),
			func(id string) (string, error) { return id, nil }))
		app.Must(web.DeleteText(base+"/{id}", web.PathString("id"),
			func(id string) (string, error) { return id, nil }))
		app.Must(web.GetText(base+"/files/{path...}", web.PathRest("path"),
			func(p string) (string, error) { return p, nil }))
	}
	return app
}

func newGinStress(n int) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	for i := 0; i < n; i++ {
		base := fmt.Sprintf("/api/v%d/res%d", i, i)
		r.GET(base, func(c *gin.Context) { c.String(200, "ok") })
		r.GET(base+"/:id", func(c *gin.Context) { c.String(200, c.Param("id")) })
		r.POST(base, func(c *gin.Context) { c.String(200, "ok") })
		r.PUT(base+"/:id", func(c *gin.Context) { c.String(200, c.Param("id")) })
		r.DELETE(base+"/:id", func(c *gin.Context) { c.String(200, c.Param("id")) })
		r.GET(base+"/files/*path", func(c *gin.Context) { c.String(200, c.Param("path")) })
	}
	return r
}

var stressApp http.Handler
var ginStressApp http.Handler

func init() {
	start := time.Now()
	stressApp = newWebStress(stressN)
	webReg = time.Since(start)
	start = time.Now()
	ginStressApp = newGinStress(stressN)
	ginReg = time.Since(start)
}

var webReg, ginReg time.Duration

func TestStressRegistrationTime(t *testing.T) {
	t.Logf("web 注册 %d 路由: %v", stressN*6, webReg)
	t.Logf("gin 注册 %d 路由: %v", stressN*6, ginReg)
}

// 正确性：两框架在压力场景下状态码一致
func TestStressStatusParity(t *testing.T) {
	longParam := strings.Repeat("x", 2000)
	longPath := "/api/v1/res1" + strings.Repeat("/deep", 20)
	cases := []struct {
		method, path string
		wantWeb      int
		wantGin      int
	}{
		{"GET", "/api/v12345/res12345", 200, 200},
		{"GET", "/api/v12345/res12345/abc", 200, 200},
		{"GET", "/api/v12345/res12345/" + longParam, 200, 200},
		{"POST", "/api/v12345/res12345", 200, 200},
		// 方法不匹配：web 405 + Allow，gin 默认 404（行为差异，各断言各的）
		{"DELETE", "/api/v12345/res12345", 405, 404},
		{"GET", "/api/v12345/res12345/extra/deep", 404, 404},
		{"GET", "/api/v999999/res1", 404, 404},
		{"GET", longPath, 404, 404},
		{"GET", "/api/v12345/res12345/files/a/b/c/d.txt", 200, 200},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		stressApp.ServeHTTP(w, req)
		if w.Code != tc.wantWeb {
			t.Errorf("web %s %s = %d, want %d", tc.method, tc.path[:min(40, len(tc.path))], w.Code, tc.wantWeb)
		}
		gw := httptest.NewRecorder()
		greq := httptest.NewRequest(tc.method, tc.path, nil)
		ginStressApp.ServeHTTP(gw, greq)
		if gw.Code != tc.wantGin {
			t.Errorf("gin %s %s = %d, want %d", tc.method, tc.path[:min(40, len(tc.path))], gw.Code, tc.wantGin)
		}
	}
}

var longParamID = strings.Repeat("x", 2000)
var longMissPath = "/api/v1/res1" + strings.Repeat("/deep", 20)

func BenchmarkWebStressStatic(b *testing.B) { bench(b, stressApp, "/api/v12345/res12345") }
func BenchmarkGinStressStatic(b *testing.B) { bench(b, ginStressApp, "/api/v12345/res12345") }
func BenchmarkWebStressParam(b *testing.B)  { bench(b, stressApp, "/api/v12345/res12345/abc") }
func BenchmarkGinStressParam(b *testing.B)  { bench(b, ginStressApp, "/api/v12345/res12345/abc") }
func BenchmarkWebStressLongParam(b *testing.B) {
	bench(b, stressApp, "/api/v12345/res12345/"+longParamID)
}
func BenchmarkGinStressLongParam(b *testing.B) {
	bench(b, ginStressApp, "/api/v12345/res12345/"+longParamID)
}
func BenchmarkWebStressMethod405(b *testing.B) {
	bench405(b, stressApp, "DELETE", "/api/v12345/res12345")
}
func BenchmarkGinStressMethod405(b *testing.B) {
	bench405(b, ginStressApp, "DELETE", "/api/v12345/res12345")
}
func BenchmarkWebStressMissDeep(b *testing.B) { bench(b, stressApp, "/api/v12345/res12345/extra/deep") }
func BenchmarkGinStressMissDeep(b *testing.B) {
	bench(b, ginStressApp, "/api/v12345/res12345/extra/deep")
}
func BenchmarkWebStressMissBranch(b *testing.B) { bench(b, stressApp, "/api/v999999/res1") }
func BenchmarkGinStressMissBranch(b *testing.B) { bench(b, ginStressApp, "/api/v999999/res1") }
func BenchmarkWebStressLongPath(b *testing.B)   { bench(b, stressApp, longMissPath) }
func BenchmarkGinStressLongPath(b *testing.B)   { bench(b, ginStressApp, longMissPath) }
func BenchmarkWebStressWildcard(b *testing.B) {
	bench(b, stressApp, "/api/v12345/files/a/b/c/d.txt")
}
func BenchmarkGinStressWildcard(b *testing.B) {
	bench(b, ginStressApp, "/api/v12345/files/a/b/c/d.txt")
}

func bench405(b *testing.B, h http.Handler, method, path string) {
	w := &nullWriter{}
	req := httptest.NewRequest(method, path, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, req)
	}
}
