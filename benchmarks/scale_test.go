package benchmarks

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	web "example.com/web"
)

// 路由规模化：N 条资源路由（静态列表 + 参数单项）下的查找成本。
// 命中静态、命中参数、未命中三类请求。

func newWebScale(n int) *web.App {
	app := web.New()
	for i := 0; i < n; i++ {
		base := fmt.Sprintf("/api/v1/res%d", i)
		app.Must(web.Handle(web.Get(base), web.NoIn(), web.Text(),
			func(web.None) (string, error) { return "ok", nil }))
		app.Must(web.GetText(base+"/{id}", web.PathString("id"),
			func(id string) (string, error) { return id, nil }))
	}
	return app
}

func newGinScale(n int) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	for i := 0; i < n; i++ {
		base := fmt.Sprintf("/api/v1/res%d", i)
		r.GET(base, func(c *gin.Context) { c.String(200, "ok") })
		r.GET(base+"/:id", func(c *gin.Context) { c.String(200, c.Param("id")) })
	}
	return r
}

func benchScale(b *testing.B, h http.Handler, target string) {
	bench(b, h, target)
}

func BenchmarkWebScale200Static(b *testing.B)  { benchScale(b, newWebScale(200), "/api/v1/res37") }
func BenchmarkGinScale200Static(b *testing.B)  { benchScale(b, newGinScale(200), "/api/v1/res37") }
func BenchmarkWebScale200Param(b *testing.B)   { benchScale(b, newWebScale(200), "/api/v1/res37/123") }
func BenchmarkGinScale200Param(b *testing.B)   { benchScale(b, newGinScale(200), "/api/v1/res37/123") }
func BenchmarkWebScale1000Static(b *testing.B) { benchScale(b, newWebScale(1000), "/api/v1/res437") }
func BenchmarkGinScale1000Static(b *testing.B) { benchScale(b, newGinScale(1000), "/api/v1/res437") }
func BenchmarkWebScale1000Param(b *testing.B)  { benchScale(b, newWebScale(1000), "/api/v1/res437/123") }
func BenchmarkGinScale1000Param(b *testing.B)  { benchScale(b, newGinScale(1000), "/api/v1/res437/123") }
func BenchmarkWebScale5000Static(b *testing.B) { benchScale(b, newWebScale(5000), "/api/v1/res3437") }
func BenchmarkGinScale5000Static(b *testing.B) { benchScale(b, newGinScale(5000), "/api/v1/res3437") }
func BenchmarkWebScale5000Param(b *testing.B) {
	benchScale(b, newWebScale(5000), "/api/v1/res3437/123")
}
func BenchmarkGinScale5000Param(b *testing.B) {
	benchScale(b, newGinScale(5000), "/api/v1/res3437/123")
}
func BenchmarkWebScale5000Miss(b *testing.B) { benchScale(b, newWebScale(5000), "/api/v1/resxyz") }
func BenchmarkGinScale5000Miss(b *testing.B) { benchScale(b, newGinScale(5000), "/api/v1/resxyz") }
