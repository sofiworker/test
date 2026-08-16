package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Serve listens on addr with graceful shutdown on SIGINT/SIGTERM: in-flight
// requests get shutdownTimeout to finish, then the server closes. It returns
// nil on a clean signal-driven shutdown, so main can just log.Fatal it.
func (a *App) Serve(addr string) error { return Serve(addr, a) }

// Serve listens on addr with graceful shutdown (see App.Serve).
func Serve(addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
