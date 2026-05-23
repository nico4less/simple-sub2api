package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestServeWithGracefulShutdownStopsTunnelOnSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	httpServer := newFakeHTTPServer(nil)
	stopper := &fakeTunnelStopper{}
	done := make(chan error, 1)
	go func() {
		done <- serveWithGracefulShutdown(ctx, httpServer, stopper, nil)
	}()
	httpServer.waitStarted(t)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveWithGracefulShutdown() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveWithGracefulShutdown() did not return")
	}
	if httpServer.shutdowns != 1 {
		t.Fatalf("shutdowns = %d, want 1", httpServer.shutdowns)
	}
	if stopper.stops != 1 {
		t.Fatalf("tunnel stops = %d, want 1", stopper.stops)
	}
}

func TestServeWithGracefulShutdownStopsTunnelOnListenError(t *testing.T) {
	wantErr := errors.New("listen failed")
	httpServer := newFakeHTTPServer(wantErr)
	stopper := &fakeTunnelStopper{}
	err := serveWithGracefulShutdown(context.Background(), httpServer, stopper, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("serveWithGracefulShutdown() error = %v, want %v", err, wantErr)
	}
	if stopper.stops != 1 {
		t.Fatalf("tunnel stops = %d, want 1", stopper.stops)
	}
}

func TestServeWithGracefulShutdownStopsTunnelOnServerClosed(t *testing.T) {
	httpServer := newFakeHTTPServer(http.ErrServerClosed)
	stopper := &fakeTunnelStopper{}
	if err := serveWithGracefulShutdown(context.Background(), httpServer, stopper, nil); err != nil {
		t.Fatalf("serveWithGracefulShutdown() error = %v", err)
	}
	if stopper.stops != 1 {
		t.Fatalf("tunnel stops = %d, want 1", stopper.stops)
	}
}

type fakeHTTPServer struct {
	mu          sync.Mutex
	started     chan struct{}
	closed      chan struct{}
	listenErr   error
	shutdowns   int
	startedOnce sync.Once
	closedOnce  sync.Once
}

func newFakeHTTPServer(listenErr error) *fakeHTTPServer {
	return &fakeHTTPServer{started: make(chan struct{}), closed: make(chan struct{}), listenErr: listenErr}
}

func (f *fakeHTTPServer) ListenAndServe() error {
	f.startedOnce.Do(func() { close(f.started) })
	if f.listenErr != nil {
		return f.listenErr
	}
	<-f.closed
	return http.ErrServerClosed
}

func (f *fakeHTTPServer) Shutdown(context.Context) error {
	f.mu.Lock()
	f.shutdowns++
	f.mu.Unlock()
	f.closedOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeHTTPServer) waitStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("fake HTTP server did not start")
	}
}

type fakeTunnelStopper struct {
	stops int
}

func (f *fakeTunnelStopper) StopTunnel() error {
	f.stops++
	return nil
}
