package web

import (
	"log"
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
