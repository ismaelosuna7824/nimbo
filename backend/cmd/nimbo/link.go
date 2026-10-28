package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

// daemonLink is the daemon an MCP session talks to (mcp.Endpoint). It uses the one daemon.json names (the desktop
// app's engine, or another session's), starts one in-process when none answers, and follows daemon.json when it
// changes: an agent started before the app moves to the app's engine, so it sees what the user has open, and stops
// its own engine so one engine owns the data directory. When the app quits, the next call starts an in-process one.
type daemonLink struct {
	home   string
	logger *slog.Logger
	// start runs an in-process daemon (startDaemon) and returns its URL and how to stop it.
	start func() (url string, stop func(), err error)

	mu      sync.Mutex
	url     string
	ownURL  string // the in-process daemon, when this session runs one
	ownStop func()
	seen    daemonFile // daemon.json as last read
}

func (l *daemonLink) URL(context.Context) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if df, ok := readDaemonFile(l.home); ok && df != l.seen {
		l.seen = df
		if df.URL != l.url && reachable(df.URL) {
			l.use(df.URL)
		}
	}
	if l.url == "" {
		url, stop, err := l.start()
		if err != nil {
			return "", fmt.Errorf("start in-process daemon: %w", err)
		}
		l.url, l.ownURL, l.ownStop = url, url, stop
		l.seen, _ = readDaemonFile(l.home)
	}
	return l.url, nil
}

// use switches to url and stops the in-process daemon, if any, in the background.
func (l *daemonLink) use(url string) {
	l.url = url
	if l.ownStop != nil && l.ownURL != url {
		l.logger.Warn("switching to the running Nimbo engine", "url", url, "stopped", l.ownURL)
		go l.ownStop()
		l.ownURL, l.ownStop = "", nil
	}
}

func (l *daemonLink) Unreachable(url string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if url == l.url {
		l.url, l.seen = "", daemonFile{}
	}
}

// close stops the in-process daemon, if any.
func (l *daemonLink) close() {
	l.mu.Lock()
	stop := l.ownStop
	l.ownURL, l.ownStop = "", nil
	l.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func readDaemonFile(home string) (daemonFile, bool) {
	data, err := os.ReadFile(daemonPath(home))
	if err != nil {
		return daemonFile{}, false
	}
	var df daemonFile
	if json.Unmarshal(data, &df) != nil || df.URL == "" {
		return daemonFile{}, false
	}
	return df, true
}
