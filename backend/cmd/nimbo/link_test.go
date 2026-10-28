package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func fakeEngine(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"test"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// An agent session started before the desktop app follows the app's engine once it starts (stopping its own), and
// starts one again when the app quits.
func TestDaemonLinkFollowsTheAppEngine(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	var starts, stops atomic.Int32
	link := &daemonLink{home: home, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), start: func() (string, func(), error) {
		starts.Add(1)
		own := fakeEngine(t)
		if err := writeDaemonFile(home, own.URL); err != nil {
			return "", nil, err
		}
		return own.URL, func() { stops.Add(1); own.Close() }, nil
	}}
	defer link.close()

	own, err := link.URL(ctx)
	if err != nil || starts.Load() != 1 {
		t.Fatalf("first URL = %q, %v; starts %d", own, err, starts.Load())
	}
	if again, _ := link.URL(ctx); again != own || starts.Load() != 1 {
		t.Fatalf("second URL = %q (starts %d), want the same in-process engine", again, starts.Load())
	}

	app := fakeEngine(t)
	if err := writeDaemonFile(home, app.URL); err != nil {
		t.Fatal(err)
	}
	if got, _ := link.URL(ctx); got != app.URL {
		t.Fatalf("after the app started: %q, want %q", got, app.URL)
	}
	deadline := time.Now().Add(2 * time.Second)
	for stops.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if stops.Load() != 1 {
		t.Fatalf("own engine stopped %d times, want 1", stops.Load())
	}

	// The app quits: its engine is gone and so is daemon.json.
	app.Close()
	if err := os.Remove(daemonPath(home)); err != nil {
		t.Fatal(err)
	}
	link.Unreachable(app.URL)
	got, err := link.URL(ctx)
	if err != nil || got == app.URL || starts.Load() != 2 {
		t.Fatalf("after the app quit: %q, %v; starts %d", got, err, starts.Load())
	}
}
