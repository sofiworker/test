package web

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"example.com/web/httperr"
)

// Recover converts handler panics into a 500 response with the stack trace
// logged. It must be the outermost middleware to be effective.
func Recover() Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("web: panic: %v\n%s", r, debug.Stack())
					if !c.wroteHeader {
						writeJSONError(c, 500, "internal server error")
					}
				}
			}()
			return next(c)
		}
	}
}

// Logger logs one line per request after the handler returns, including the
// typed error value if any. It reports the EFFECTIVE status code — the error
// mapping happens after the chain unwinds, so c.Status() alone would still
// read 200 on the error path.
func Logger(l *log.Logger) Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			start := time.Now()
			err := next(c)
			l.Printf("%s %s %d %s err=%v", c.Method(), c.Path(), statusCode(c, err), time.Since(start), err)
			return err
		}
	}
}

// RequestID reads X-Request-Id (or generates one with gen) and stores it
// under the typed key plus the response header. gen may be web.NewID.
func RequestID(key Key[string], gen func() string) Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			id := c.Req.Header.Get("X-Request-Id")
			if id == "" {
				id = gen()
			}
			key.Set(c, id)
			c.Header().Set("X-Request-Id", id)
			return next(c)
		}
	}
}

// NewID returns a random 16-hex request id.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Timeout bounds the request's cancellation context. Handlers observe it via
// the req.Context() / r.Context() accessor or any context-taking API.
func Timeout(d time.Duration) Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			ctx, cancel := context.WithTimeout(c.Req.Context(), d)
			defer cancel()
			c.Req = c.Req.WithContext(ctx)
			return next(c)
		}
	}
}

// BodyLimit caps the request body size; reads beyond the limit fail, which
// the BodyJSON descriptor maps to 400.
func BodyLimit(n int64) Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			c.Req.Body = http.MaxBytesReader(c.W, c.Req.Body, n)
			return next(c)
		}
	}
}

// CORSConfig declares CORS behavior explicitly.
type CORSConfig struct {
	AllowOrigins     []string // "*" or explicit origins
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	MaxAgeSeconds    int
}

// CORS stamps CORS response headers on simple responses. Preflight
// (OPTIONS) is answered at the App level by UseCORS, because the router's
// automatic OPTIONS handling runs before any middleware chain — and auth
// middleware must not block preflight. Everything sent is declared in cfg —
// no defaults, no magic.
func CORS(cfg CORSConfig) Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			origin := c.Req.Header.Get("Origin")
			if ao := allowOrigin(cfg, origin); ao != "" {
				c.Header().Set("Access-Control-Allow-Origin", ao)
				if cfg.AllowCredentials {
					c.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				c.Header().Set("Vary", "Origin")
			}
			return next(c)
		}
	}
}

func allowOrigin(cfg CORSConfig, origin string) string {
	for _, o := range cfg.AllowOrigins {
		if o == "*" || o == origin {
			return o
		}
	}
	return ""
}

// Compress gzips responses when the client accepts it, streaming: writes are
// compressed on the fly and Content-Length is dropped. No buffering.
func Compress() Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			if !strings.Contains(c.Req.Header.Get("Accept-Encoding"), "gzip") {
				return next(c)
			}
			gz := gzipPool.Get().(*gzip.Writer)
			gz.Reset(c.W)
			c.Header().Set("Content-Encoding", "gzip")
			c.Header().Del("Content-Length")
			c.W = &gzipResponseWriter{ResponseWriter: c.W, gz: gz}
			err := next(c)
			_ = gz.Close()
			gzipPool.Put(gz)
			return err
		}
	}
}

var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) { return w.gz.Write(b) }

// Flush flushes the gzip stream if the underlying writer supports it.
func (w *gzipResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		_ = w.gz.Flush()
		f.Flush()
	}
}

// CacheControl stamps Cache-Control: public, max-age=N.
func CacheControl(maxAgeSeconds int) Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			c.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAgeSeconds))
			return next(c)
		}
	}
}

// NoCache stamps Cache-Control: no-store.
func NoCache() Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			c.Header().Set("Cache-Control", "no-store")
			return next(c)
		}
	}
}

// RateLimit caps requests with an in-memory token bucket per key (client IP
// by default; key may be a header or any request-derived string). Exceeding
// the limit yields 429. The bucket table is process-local and unbounded —
// for horizontal scale or long-term retention, back this with external
// storage instead.
func RateLimit(rps float64, burst int, key func(*Ctx) string) Middleware {
	if key == nil {
		key = func(c *Ctx) string { return c.Req.RemoteAddr }
	}
	var mu sync.Mutex
	buckets := map[string]*tokenBucket{}
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			k := key(c)
			mu.Lock()
			b := buckets[k]
			if b == nil {
				b = &tokenBucket{tokens: float64(burst)}
				buckets[k] = b
			}
			now := time.Now()
			elapsed := now.Sub(b.last).Seconds()
			b.last = now
			b.tokens = min(float64(burst), b.tokens+elapsed*rps)
			if b.tokens < 1 {
				mu.Unlock()
				return httperr.TooManyRequests()
			}
			b.tokens--
			mu.Unlock()
			return next(c)
		}
	}
}

type tokenBucket struct {
	last   time.Time
	tokens float64
}
