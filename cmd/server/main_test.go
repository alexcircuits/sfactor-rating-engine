package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sfactor/scoring/internal/httpapi"
)

// The server should answer health checks and stop cleanly when its context is cancelled.
func TestServeAnswersUntilItsContextEnds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() {
		stopped <- serve(ctx, ln, httpapi.New(httpapi.Options{}), slog.New(slog.DiscardHandler))
	}()

	resp, err := http.Get("http://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("serve returned %v after its context ended, want a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after its context ended")
	}
}

func TestRunReportsAPortInUse(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })

	if err := run(taken.Addr().String(), httpapi.Options{}, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("run on a port already in use returned no error")
	}
}
