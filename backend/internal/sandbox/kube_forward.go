package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The daemon forwards every TCP port of the workflow's Services from a
// stable 127.0.0.1 port: the first allocation prefers the Service port
// itself (80 → 8080, 443 → 8443 below 1024) and is saved in
// <kube dir>/forwards.json, so the same Service port keeps its host port
// across ups, cluster restarts and daemon restarts while it is free. Each
// accepted connection goes to a ready pod behind the Service, picked when
// it connects (rollouts and restarts need no new forward).

// upstream is one forwarded connection's remote side.
type upstream interface {
	io.ReadWriter
	CloseWrite() error // no more data to the remote side
	Close() error
}

// connector opens an upstream to a ready pod behind a Service port.
type connector interface {
	connect(ctx context.Context, ns, svc, portName string) (upstream, error)
	// close releases the connector's connections.
	close()
}

// forwarder runs the port-forwards of one workflow.
type forwarder struct {
	dir string

	mu     sync.Mutex
	conn   connector
	active map[string]*portForward
	saved  map[string]int // forward key -> host port
	loaded bool
	// free probes a host port; reserved lists other workflows' saved
	// ports (tests replace both).
	free     func(int) bool
	reserved func() map[int]bool
}

type portForward struct {
	spec     forwardSpec
	hostPort int
	ln       net.Listener
	cancel   context.CancelFunc
	lastErr  atomic.Value // string: the last connection failure
}

func newForwarder(dir string) *forwarder {
	f := &forwarder{dir: dir, active: map[string]*portForward{}, saved: map[string]int{}, free: portFree}
	f.reserved = func() map[int]bool {
		return otherForwardPorts(filepath.Dir(filepath.Dir(dir)), filepath.Base(filepath.Dir(dir)))
	}
	return f
}

func (f *forwarder) setConnector(c connector) {
	f.mu.Lock()
	f.conn = c
	f.mu.Unlock()
}

func (f *forwarder) path() string { return filepath.Join(f.dir, "forwards.json") }

func (f *forwarder) load() {
	if f.loaded {
		return
	}
	f.loaded = true
	if data, err := os.ReadFile(f.path()); err == nil {
		_ = json.Unmarshal(data, &f.saved)
	}
	if f.saved == nil {
		f.saved = map[string]int{}
	}
}

func (f *forwarder) save() {
	data, err := json.MarshalIndent(f.saved, "", "  ")
	if err == nil {
		_ = writeFile(f.path(), data)
	}
}

// otherForwardPorts are the host ports saved by the other workflows'
// forwarders (workflowsDir/<wf>/kube/forwards.json), so two workflows do
// not trade ports while one of them is stopped.
func otherForwardPorts(workflowsDir, self string) map[int]bool {
	out := map[int]bool{}
	paths, _ := filepath.Glob(filepath.Join(workflowsDir, "*", kubeWorkDir, "forwards.json"))
	for _, p := range paths {
		if filepath.Base(filepath.Dir(filepath.Dir(p))) == self {
			continue
		}
		var saved map[string]int
		if data, err := os.ReadFile(p); err == nil && json.Unmarshal(data, &saved) == nil {
			for _, hp := range saved {
				out[hp] = true
			}
		}
	}
	return out
}

// preferredPort is the host port tried first for a Service port.
func preferredPort(port int) int {
	if port < 1024 {
		return port + 8000
	}
	return port
}

// allocate picks the host port of key: the saved one, else the preferred
// one, else any free port — never one saved for another key or workflow.
func (f *forwarder) allocate(key string, port int) (net.Listener, int, error) {
	taken := map[int]bool{}
	for k, hp := range f.saved {
		if k != key {
			taken[hp] = true
		}
	}
	reserved := f.reserved()
	try := func(hp int) net.Listener {
		if hp <= 0 || hp > 65535 || taken[hp] || !f.free(hp) {
			return nil
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(hp))
		if err != nil {
			return nil
		}
		return ln
	}
	if hp, ok := f.saved[key]; ok {
		if ln := try(hp); ln != nil {
			return ln, hp, nil
		}
	}
	if hp := preferredPort(port); !reserved[hp] {
		if ln := try(hp); ln != nil {
			return ln, hp, nil
		}
	}
	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, 0, err
		}
		hp := ln.Addr().(*net.TCPAddr).Port
		if !taken[hp] && !reserved[hp] {
			return ln, hp, nil
		}
		ln.Close()
	}
	return nil, 0, errors.New("no free local port")
}

// sync starts the forwards of specs, stops the others and returns their
// state, sorted like specs.
func (f *forwarder) sync(specs []forwardSpec) []model.KubeForward {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.load()
	want := map[string]bool{}
	out := make([]model.KubeForward, 0, len(specs))
	dirty := false
	for _, s := range specs {
		want[s.key] = true
		pf := f.active[s.key]
		if pf == nil {
			ln, hp, err := f.allocate(s.key, s.port)
			if err != nil {
				out = append(out, forwardState(s, 0, "No local port: "+err.Error()))
				continue
			}
			if f.saved[s.key] != hp {
				f.saved[s.key] = hp
				dirty = true
			}
			ctx, cancel := context.WithCancel(context.Background())
			pf = &portForward{spec: s, hostPort: hp, ln: ln, cancel: cancel}
			f.active[s.key] = pf
			go f.serve(ctx, pf)
		}
		pf.spec = s
		msg, _ := pf.lastErr.Load().(string)
		if !s.ready {
			msg = "No ready pod behind the Service yet: connections are refused until one is ready."
		}
		out = append(out, forwardState(s, pf.hostPort, msg))
	}
	for key, pf := range f.active {
		if !want[key] {
			pf.cancel()
			pf.ln.Close()
			delete(f.active, key)
		}
	}
	if dirty {
		f.save()
	}
	return out
}

func forwardState(s forwardSpec, hostPort int, msg string) model.KubeForward {
	fw := model.KubeForward{
		NodeID: s.nodeID, Namespace: s.namespace, Service: s.service, Port: s.port, PortName: s.portName,
		Protocol: "TCP", HostPort: hostPort, Ready: s.ready && hostPort > 0, Message: msg,
	}
	if s.http && hostPort > 0 {
		fw.URL = fmt.Sprintf("http://127.0.0.1:%d/", hostPort)
	}
	return fw
}

// stopAll closes every listener and the connector (cluster stopped or
// deleted).
func (f *forwarder) stopAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, pf := range f.active {
		pf.cancel()
		pf.ln.Close()
		delete(f.active, key)
	}
	if f.conn != nil {
		f.conn.close()
		f.conn = nil
	}
}

// hostPort returns the active host port of a forward key (0 = none).
func (f *forwarder) hostPort(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pf := f.active[key]; pf != nil {
		return pf.hostPort
	}
	return 0
}

func (f *forwarder) serve(ctx context.Context, pf *portForward) {
	for {
		c, err := pf.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(ctx, pf, c)
	}
}

func (f *forwarder) handle(ctx context.Context, pf *portForward, local net.Conn) {
	defer local.Close()
	f.mu.Lock()
	conn, s := f.conn, pf.spec
	f.mu.Unlock()
	if conn == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	up, err := conn.connect(cctx, s.namespace, s.service, s.portName)
	cancel()
	if err != nil {
		pf.lastErr.Store(err.Error())
		return
	}
	pf.lastErr.Store("")
	defer up.Close()
	remoteDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(local, up)
		close(remoteDone)
	}()
	go func() {
		_, _ = io.Copy(up, local)
		_ = up.CloseWrite()
	}()
	select {
	case <-remoteDone:
	case <-ctx.Done():
	}
}

// ---------------------------------------------------------------------------
// Kubernetes port-forward streams

// kubeConnector opens port-forward streams (WebSocket tunnel, SPDY
// fallback, like kubectl) to the pods behind a Service, one connection per
// pod reused across requests.
type kubeConnector struct {
	kc      *kubeClient
	listers kubeListers
	state   *connectorState
}

type connectorState struct {
	mu    sync.Mutex
	conns map[string]httpstream.Connection // namespace/pod
	reqID atomic.Int64
	// next rotates connections over the ready pods of a Service.
	next atomic.Uint64
}

func newKubeConnector(kc *kubeClient, l kubeListers) kubeConnector {
	return kubeConnector{kc: kc, listers: l, state: &connectorState{conns: map[string]httpstream.Connection{}}}
}

func (c kubeConnector) connect(ctx context.Context, ns, svc, portName string) (upstream, error) {
	pod, port, ok := readyEndpoint(c.listers.endpointSlices(ns, svc), portName, c.state.next.Add(1)-1)
	if !ok {
		return nil, fmt.Errorf("no ready pod behind Service %s/%s", ns, svc)
	}
	sc, err := c.podConn(ctx, ns, pod)
	if err != nil {
		return nil, fmt.Errorf("port-forward to pod %s: %w", pod, err)
	}
	id := strconv.FormatInt(c.state.reqID.Add(1), 10)
	headers := http.Header{}
	headers.Set(corev1.StreamType, corev1.StreamTypeError)
	headers.Set(corev1.PortHeader, strconv.Itoa(port))
	headers.Set(corev1.PortForwardRequestIDHeader, id)
	errStream, err := sc.CreateStream(headers)
	if err != nil {
		c.drop(ns, pod, sc)
		return nil, err
	}
	_ = errStream.Close() // nothing is written to it
	headers.Set(corev1.StreamType, corev1.StreamTypeData)
	data, err := sc.CreateStream(headers)
	if err != nil {
		sc.RemoveStreams(errStream)
		c.drop(ns, pod, sc)
		return nil, err
	}
	go func() { _, _ = io.Copy(io.Discard, errStream) }()
	return &podStream{conn: sc, data: data, errStream: errStream}, nil
}

func (c kubeConnector) podConn(ctx context.Context, ns, pod string) (httpstream.Connection, error) {
	key := ns + "/" + pod
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if sc := c.state.conns[key]; sc != nil {
		select {
		case <-sc.CloseChan():
			delete(c.state.conns, key)
		default:
			return sc, nil
		}
	}
	u := c.kc.cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("portforward").URL()
	rt, upgrader, err := spdy.RoundTripperFor(c.kc.rest)
	if err != nil {
		return nil, err
	}
	spdyDialer := spdy.NewDialer(upgrader, &http.Client{Transport: rt}, http.MethodPost, u)
	wsDialer, err := portforward.NewSPDYOverWebsocketDialer(u, c.kc.rest)
	if err != nil {
		return nil, err
	}
	dialer := portforward.NewFallbackDialer(wsDialer, spdyDialer, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	type result struct {
		sc  httpstream.Connection
		err error
	}
	ch := make(chan result, 1)
	go func() {
		sc, _, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
		ch <- result{sc, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		c.state.conns[key] = r.sc
		return r.sc, nil
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.sc != nil {
				r.sc.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

func (c kubeConnector) drop(ns, pod string, sc httpstream.Connection) {
	c.state.mu.Lock()
	if c.state.conns[ns+"/"+pod] == sc {
		delete(c.state.conns, ns+"/"+pod)
	}
	c.state.mu.Unlock()
	sc.Close()
}

// close closes the pod connections.
func (c kubeConnector) close() {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	for k, sc := range c.state.conns {
		sc.Close()
		delete(c.state.conns, k)
	}
}

// podStream is one forwarded connection over a pod's port-forward
// connection.
type podStream struct {
	conn      httpstream.Connection
	data      httpstream.Stream
	errStream httpstream.Stream
	once      sync.Once
}

func (s *podStream) Read(p []byte) (int, error)  { return s.data.Read(p) }
func (s *podStream) Write(p []byte) (int, error) { return s.data.Write(p) }
func (s *podStream) CloseWrite() error           { return s.data.Close() }

func (s *podStream) Close() error {
	s.once.Do(func() {
		_ = s.data.Reset()
		s.conn.RemoveStreams(s.data, s.errStream)
	})
	return nil
}

// keys lists the active forward keys, sorted.
func (f *forwarder) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.active))
	for k := range f.active {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
