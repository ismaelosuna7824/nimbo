// Command nimbo is the Nimbo daemon: an HTTP API for the desktop app
// (`nimbo serve`) and an MCP stdio server for AI agents (`nimbo mcp`).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/agents"
	"github.com/ismaelosuna7824/nimbo/backend/internal/api"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/events"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/mcp"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
	"github.com/ismaelosuna7824/nimbo/backend/internal/settings"
	"github.com/ismaelosuna7824/nimbo/backend/internal/setup"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage:
  nimbo serve [--addr 127.0.0.1:7788] [--home DIR] [--exit-with-parent]
                                                     run the HTTP daemon
  nimbo mcp [--home DIR]                             run the MCP stdio server
  nimbo doctor [--json] [--home DIR]                 check this machine's setup (exit 1 if not ready)
  nimbo version                                      print the version

The home directory defaults to $NIMBO_HOME or ~/.nimbo.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		setup.AugmentPath()
		err = serveCmd(os.Args[2:])
	case "mcp":
		setup.AugmentPath()
		err = mcpCmd(os.Args[2:])
	case "doctor":
		setup.AugmentPath()
		err = doctorCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nimbo:", err)
		os.Exit(1)
	}
}

// resolveHome returns the data directory and whether the legacy pre-rename
// home was migrated into it on this start.
func resolveHome(flagValue string) (string, bool, error) {
	home := flagValue
	if home == "" {
		home = os.Getenv("NIMBO_HOME")
	}
	migrated := false
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
		home = filepath.Join(userHome, ".nimbo")
		if migrated, err = migrateLegacyHome(userHome, home); err != nil {
			return "", false, err
		}
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", false, err
	}
	return home, migrated, os.MkdirAll(home, 0o700)
}

// daemon is a running HTTP API server.
type daemon struct {
	url    string
	home   string
	srv    *http.Server
	cancel context.CancelFunc
	done   chan error
	logger *slog.Logger
}

// startDaemon wires every component and serves the API on addr, which must
// be a loopback address (port 0 picks a free port).
func startDaemon(home, addr string, legacyMigrated bool, logger *slog.Logger) (*daemon, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("--addr must be a loopback address, got %q", addr)
	}

	st, err := store.Open(home)
	if err != nil {
		return nil, err
	}
	if n, err := st.AssignMissingIcons(); err != nil {
		logger.Warn("assign workflow icons", "err", err)
	} else if n > 0 {
		logger.Info("assigned icons to workflows", "count", n)
	}
	cat, err := catalog.Load()
	if err != nil {
		return nil, fmt.Errorf("load catalog: %w", err)
	}
	prefs, err := settings.Open(home)
	if err != nil {
		return nil, err
	}
	userPrefs := prefs.Get()
	cat.SetDefaultAWSRegion(userPrefs.DefaultAWSRegion)
	agentEnv, err := agents.DefaultEnv()
	if err != nil {
		return nil, err
	}
	hub := events.NewHub()
	sb, err := sandbox.New(sandbox.Config{
		Home: home, Events: hub, LegacyHomeMigrated: legacyMigrated,
		Docker: userPrefs.Docker, Images: userPrefs.Images, TerraformImage: userPrefs.TerraformImage,
		AWSRegion: userPrefs.DefaultAWSRegion,
	})
	if err != nil {
		return nil, err
	}
	conns, err := store.OpenConnections(home)
	if err != nil {
		return nil, err
	}
	engine := loadtest.NewEngine(loadtest.EngineConfig{Home: home, Events: hub, Resolver: sb, Connections: conns, Monitor: sb})
	server := api.New(api.Config{
		Version: version, Home: home, Store: st, Connections: conns, Catalog: cat,
		Sandbox: sb, LoadTest: engine, Events: hub, Logger: logger,
		Settings: prefs, Agents: agents.New(agentEnv),
		MCPCatalog: func() api.MCPCatalog {
			return api.MCPCatalog{Tools: mcp.ToolCatalog(), Prompts: mcp.PromptCatalog()}
		},
		Setup: setup.New(setup.Config{Home: home, Settings: prefs.Get, Events: hub}),
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	// Cancelling the base context ends long-lived SSE streams on shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	d := &daemon{
		url:    "http://" + ln.Addr().String(),
		home:   home,
		cancel: cancel,
		done:   make(chan error, 1),
		logger: logger,
		srv: &http.Server{
			Handler:           server.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return ctx },
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		},
	}
	go func() {
		err := d.srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		d.done <- err
	}()
	if err := writeDaemonFile(home, d.url); err != nil {
		d.shutdown()
		return nil, err
	}
	return d, nil
}

func (d *daemon) shutdown() {
	d.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.srv.Shutdown(ctx); err != nil {
		d.logger.Warn("shutdown", "err", err)
	}
	removeDaemonFile(d.home, d.url)
}

type daemonFile struct {
	URL string `json:"url"`
	PID int    `json:"pid"`
}

func daemonPath(home string) string { return filepath.Join(home, "daemon.json") }

func writeDaemonFile(home, url string) error {
	data, err := json.Marshal(daemonFile{URL: url, PID: os.Getpid()})
	if err != nil {
		return err
	}
	tmp := daemonPath(home) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, daemonPath(home))
}

// removeDaemonFile deletes daemon.json only if it still describes this
// process, so a newer daemon's discovery file survives.
func removeDaemonFile(home, url string) {
	data, err := os.ReadFile(daemonPath(home))
	if err != nil {
		return
	}
	var df daemonFile
	if json.Unmarshal(data, &df) == nil && df.URL == url && df.PID == os.Getpid() {
		os.Remove(daemonPath(home))
	}
}

func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:7788", "listen address (loopback only; port 0 = any free port)")
	homeFlag := fs.String("home", "", "data directory (default $NIMBO_HOME or ~/.nimbo)")
	exitWithParent := fs.Bool("exit-with-parent", false, "shut down when stdin reaches EOF (the parent process exited)")
	fs.Parse(args)
	if *exitWithParent {
		setParentDeathSignal()
		// The parent also owns our stdout/stderr pipes. Once it is gone, a log
		// write would raise SIGPIPE and kill us before the graceful shutdown;
		// receiving SIGPIPE turns those writes into ignorable EPIPE errors.
		signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	}
	home, migrated, err := resolveHome(*homeFlag)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if migrated {
		logger.Info("migrated legacy data directory", "home", home)
	}
	d, err := startDaemon(home, *addr, migrated, logger)
	if err != nil {
		return err
	}
	fmt.Printf("NIMBO_LISTENING %s\n", d.url)
	logger.Info("nimbo listening", "url", d.url, "home", home, "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *exitWithParent {
		var cancel context.CancelFunc
		ctx, cancel = withParentExit(ctx, os.Stdin)
		defer cancel()
	}
	select {
	case <-ctx.Done():
		logger.Info("shutting down")
		d.shutdown()
		return nil
	case err := <-d.done:
		removeDaemonFile(home, d.url)
		return err
	}
}

// reachable reports whether an nimbo daemon answers /health at url.
func reachable(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(url + "/api/v1/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var h struct {
		Version string `json:"version"`
	}
	return res.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&h) == nil && h.Version != ""
}

func mcpCmd(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	homeFlag := fs.String("home", "", "data directory (default $NIMBO_HOME or ~/.nimbo)")
	fs.Parse(args)
	home, migrated, err := resolveHome(*homeFlag)
	if err != nil {
		return err
	}
	// stdout carries the MCP protocol; logs go to stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	url := ""
	if data, err := os.ReadFile(daemonPath(home)); err == nil {
		var df daemonFile
		if json.Unmarshal(data, &df) == nil && df.URL != "" && reachable(df.URL) {
			url = df.URL
		}
	}
	if url == "" {
		if migrated {
			logger.Warn("migrated legacy data directory", "home", home)
		}
		d, err := startDaemon(home, "127.0.0.1:0", migrated, logger)
		if err != nil {
			return fmt.Errorf("start in-process daemon: %w", err)
		}
		defer d.shutdown()
		url = d.url
	}
	return mcp.Run(ctx, url, version)
}
