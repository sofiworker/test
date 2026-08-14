// Package benchmarks compares the endpoint-as-data framework against gin and
// the stdlib mux on identical workloads. Run from this directory:
//
//	go test -bench . -benchmem -benchtime=1s
package benchmarks

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	web "example.com/web"
)

type payload struct {
	Message string `json:"message"`
	Num     int    `json:"num"`
}

var payloadVal = &payload{Message: "Hello, World!", Num: 42}

// nullWriter implements http.ResponseWriter with zero allocations. Pointer
// receivers keep the interface conversion allocation-free, and WriteString
// mirrors net/http's response writer. Header().Set still allocates its
// []string value — as in real net/http writers — so both frameworks pay that
// writer-side cost equally. The shared header map is safe because benchmarks
// run sequentially.
var nullHeader = http.Header{}

type nullWriter struct{}

func (*nullWriter) Header() http.Header { return nullHeader }
func (*nullWriter) Write(b []byte) (int, error) {
	return len(b), nil
}
func (*nullWriter) WriteString(s string) (int, error) { return len(s), nil }
func (*nullWriter) WriteHeader(int)                   {}

func webNoop(next web.Handler) web.Handler {
	return func(c *web.Ctx) error { return next(c) }
}

func newWebApp(withMW bool) *web.App {
	app := web.New()
	if withMW {
		app.Use(webNoop, webNoop, webNoop, webNoop, webNoop)
	}
	app.Must(web.Handle(web.Get("/hello"), web.NoIn(), web.Text(),
		func(web.None) (string, error) { return "Hello, World!", nil }))
	app.Must(web.Handle(web.Get("/json"), web.NoIn(), web.JSON[*payload](),
		func(web.None) (*payload, error) { return payloadVal, nil }))
	app.Must(web.Handle(web.Get("/user/{id}"), web.PathInt64("id"), web.JSON[*payload](),
		func(id int64) (*payload, error) { return payloadVal, nil }))
	return app
}

func newGinApp(withMW bool) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	if withMW {
		r.Use(func(c *gin.Context) {}, func(c *gin.Context) {}, func(c *gin.Context) {},
			func(c *gin.Context) {}, func(c *gin.Context) {})
	}
	r.GET("/hello", func(c *gin.Context) { c.String(200, "Hello, World!") })
	r.GET("/json", func(c *gin.Context) { c.JSON(200, payloadVal) })
	r.GET("/user/:id", func(c *gin.Context) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil {
			c.AbortWithStatus(400)
			return
		}
		_ = id
		c.JSON(200, payloadVal)
	})
	return r
}

func newStdlibMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "Hello, World!")
	})
	return mux
}

func bench(b *testing.B, h http.Handler, path string) {
	w := &nullWriter{}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, req)
	}
}

func BenchmarkWebStaticText(b *testing.B)    { bench(b, newWebApp(false), "/hello") }
func BenchmarkGinStaticText(b *testing.B)    { bench(b, newGinApp(false), "/hello") }
func BenchmarkStdlibStaticText(b *testing.B) { bench(b, newStdlibMux(), "/hello") }

func BenchmarkWebStaticJSON(b *testing.B) { bench(b, newWebApp(false), "/json") }
func BenchmarkGinStaticJSON(b *testing.B) { bench(b, newGinApp(false), "/json") }

func BenchmarkWebParamJSON(b *testing.B) { bench(b, newWebApp(false), "/user/42") }
func BenchmarkGinParamJSON(b *testing.B) { bench(b, newGinApp(false), "/user/42") }

func BenchmarkWebFiveMW(b *testing.B) { bench(b, newWebApp(true), "/hello") }
func BenchmarkGinFiveMW(b *testing.B) { bench(b, newGinApp(true), "/hello") }

func BenchmarkWebNotFound(b *testing.B) { bench(b, newWebApp(false), "/nope") }
func BenchmarkGinNotFound(b *testing.B) { bench(b, newGinApp(false), "/nope") }
