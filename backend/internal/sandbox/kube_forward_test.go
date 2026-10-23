package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// echoConnector dials a local TCP server standing in for the pod.
type echoConnector struct {
	addr   string
	calls  atomic.Int32
	closed atomic.Bool
	fail   atomic.Bool
}

func (c *echoConnector) connect(_ context.Context, ns, svc, portName string) (upstream, error) {
	c.calls.Add(1)
	if c.fail.Load() {
		return nil, fmt.Errorf("no ready pod behind Service %s/%s", ns, svc)
	}
	conn, err := net.Dial("tcp", c.addr)
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil
}

func (c *echoConnector) close() { c.closed.Store(true) }

// echoServer answers each line with "<prefix><line>".
func echoServer(t *testing.T, prefix string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					fmt.Fprintf(c, "%s%s\n", prefix, sc.Text())
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, port int, msg string) (string, error) {
	t.Helper()
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "%s\n", msg)
	line, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(line), err
}

func freePort(t *testing.T) int {
	t.Helper()
	p, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testForwarder(t *testing.T, dir string) *forwarder {
	f := newForwarder(dir)
	f.reserved = func() map[int]bool { return map[int]bool{} }
	t.Cleanup(f.stopAll)
	return f
}

func TestForwarderProxiesAndKeepsPorts(t *testing.T) {
	dir := t.TempDir()
	conn := &echoConnector{addr: echoServer(t, "pod:")}
	f := testForwarder(t, dir)
	f.setConnector(conn)

	// The preferred host port is the Service port (>= 1024).
	pref := freePort(t)
	specs := []forwardSpec{
		{key: "default/web/" + fmt.Sprint(pref), nodeID: "Service/default/web", namespace: "default", service: "web", port: pref, portName: "http", http: true, ready: true},
		{key: "default/db/5432", nodeID: "Service/default/db", namespace: "default", service: "db", port: 5432, ready: false},
	}
	out := f.sync(specs)
	if len(out) != 2 || out[0].HostPort != pref || out[0].URL != fmt.Sprintf("http://127.0.0.1:%d/", pref) || !out[0].Ready {
		t.Fatalf("forwards = %+v", out)
	}
	if out[1].URL != "" || out[1].Ready || out[1].HostPort == 0 || !strings.Contains(out[1].Message, "No ready pod") {
		t.Errorf("db forward = %+v", out[1])
	}
	if got, err := roundTrip(t, pref, "hello"); err != nil || got != "pod:hello" {
		t.Fatalf("proxied = %q, %v", got, err)
	}

	// The allocation is saved; a new forwarder (daemon restart) reuses it.
	var saved map[string]int
	data, _ := os.ReadFile(filepath.Join(dir, "forwards.json"))
	if err := json.Unmarshal(data, &saved); err != nil || saved[specs[0].key] != pref || saved["default/db/5432"] != out[1].HostPort {
		t.Fatalf("saved = %s (%v)", data, err)
	}
	f.stopAll()
	if !conn.closed.Load() {
		t.Error("stopAll did not close the connector")
	}
	if _, err := roundTrip(t, pref, "x"); err == nil {
		t.Error("listener still open after stopAll")
	}
	g := testForwarder(t, dir)
	g.setConnector(conn)
	again := g.sync(specs)
	if again[0].HostPort != pref || again[1].HostPort != out[1].HostPort {
		t.Errorf("after restart = %+v, before %+v", again, out)
	}

	// A removed Service stops its forward but keeps its saved port.
	g.sync(specs[:1])
	if keys := g.keys(); !slices.Equal(keys, []string{specs[0].key}) {
		t.Errorf("active = %v", keys)
	}
	if again := g.sync(specs); again[1].HostPort != out[1].HostPort {
		t.Errorf("re-added db got %d, want %d", again[1].HostPort, out[1].HostPort)
	}

	// Connection failures surface on the forward.
	conn.fail.Store(true)
	if _, err := roundTrip(t, pref, "x"); err == nil {
		t.Error("expected the connection to close")
	}
	time.Sleep(50 * time.Millisecond)
	if st := g.sync(specs); !strings.Contains(st[0].Message, "no ready pod") {
		t.Errorf("message = %q", st[0].Message)
	}
}

func TestForwarderAllocation(t *testing.T) {
	f := testForwarder(t, t.TempDir())
	f.setConnector(&echoConnector{addr: "127.0.0.1:1"})

	// Below 1024 the preferred port is +8000; taken ones fall back to a
	// free port; ports saved by other workflows are not taken.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	reservedPort := freePort(t)
	f.reserved = func() map[int]bool { return map[int]bool{reservedPort: true} }
	if preferredPort(80) != 8080 || preferredPort(443) != 8443 || preferredPort(3000) != 3000 {
		t.Error("preferredPort")
	}
	out := f.sync([]forwardSpec{
		{key: "a/busy", port: busyPort, ready: true},
		{key: "a/reserved", port: reservedPort, ready: true},
	})
	if out[0].HostPort == busyPort || out[0].HostPort == 0 {
		t.Errorf("busy port reused: %+v", out[0])
	}
	if out[1].HostPort == reservedPort || out[1].HostPort == 0 {
		t.Errorf("reserved port taken: %+v", out[1])
	}
	if out[0].HostPort == out[1].HostPort {
		t.Error("two forwards share a port")
	}
}

func TestOtherForwardPorts(t *testing.T) {
	root := t.TempDir()
	for wf, ports := range map[string]map[string]int{"self": {"a": 1111}, "other": {"b": 2222, "c": 3333}} {
		p := filepath.Join(root, wf, kubeWorkDir, "forwards.json")
		data, _ := json.Marshal(ports)
		if err := writeFile(p, data); err != nil {
			t.Fatal(err)
		}
	}
	got := otherForwardPorts(root, "self")
	if len(got) != 2 || !got[2222] || !got[3333] || got[1111] {
		t.Errorf("reserved = %v", got)
	}
	f := newForwarder(filepath.Join(root, "self", kubeWorkDir))
	if r := f.reserved(); !r[2222] || r[1111] {
		t.Errorf("forwarder reserved = %v", r)
	}
}

// halfCloser checks the forward propagates the client's EOF upstream.
func TestForwarderHalfClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		data, _ := io.ReadAll(c) // until the client half-closes
		fmt.Fprintf(c, "got %d bytes", len(data))
	}()
	f := testForwarder(t, t.TempDir())
	f.setConnector(&echoConnector{addr: ln.Addr().String()})
	out := f.sync([]forwardSpec{{key: "k", port: freePort(t), ready: true}})
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", out[0].HostPort))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprint(c, "12345")
	_ = c.(*net.TCPConn).CloseWrite()
	resp, err := io.ReadAll(c)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if string(resp) != "got 5 bytes" {
		t.Errorf("response = %q", resp)
	}
}
