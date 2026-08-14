package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"time"
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
