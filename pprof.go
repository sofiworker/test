package web

import (
	"net/http"
	"net/http/pprof"
)

// PProf mounts the stdlib pprof endpoints under /debug/pprof/. Mount it
// behind your own access control in production:
//
//	app.Must(web.PProf()...)
func PProf() []*Route {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// 两条路由：无尾斜杠索引（尾斜杠请求会容忍匹配到它）+ 深层路径 catch-all。
	return []*Route{
		Raw("GET", "/debug/pprof", FromStd(mux)),
		Raw("GET", "/debug/pprof/{path...}", FromStd(mux)),
	}
}
