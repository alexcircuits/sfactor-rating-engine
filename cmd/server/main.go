// Command server runs the rating HTTP API. See api/openapi.yaml for the contract.
//
//	server -port 8080 -cors-origin https://app.example
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sfactor/scoring/internal/httpapi"
)

// shutdownGrace is the time allowed for active requests to finish during shutdown.
const shutdownGrace = 30 * time.Second

func main() {
	port := flag.String("port", "8080", "port to listen on")
	corsOrigin := flag.String("cors-origin", "", "the one browser origin allowed by CORS; empty sends no CORS headers")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	opts := httpapi.Options{CORSOrigin: *corsOrigin, Logger: logger}
	if err := run(":"+*port, opts, logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// run listens on addr and serves until SIGINT or SIGTERM.
func run(addr string, opts httpapi.Options, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return serve(ctx, ln, httpapi.New(opts), logger)
}

// serve handles requests until ctx is cancelled, then waits for active requests to
// finish.
func serve(ctx context.Context, ln net.Listener, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	failed := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", ln.Addr().String())
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
