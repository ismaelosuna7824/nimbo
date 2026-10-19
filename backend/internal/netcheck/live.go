package netcheck

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// LiveSandbox is what live probes need from the sandbox (*sandbox.Manager).
type LiveSandbox interface {
	Instances(ctx context.Context, wfID string) (map[string]sandbox.InstanceAccess, error)
	ExecInstance(ctx context.Context, wfID, instanceID, script string, timeout time.Duration) (string, int, error)
}

// What the local emulator (Floci 2.3, verified; see docs/ARCHITECTURE.md
// "netcheck") enforces for EC2: nothing between containers — security
// groups, network ACLs, route tables and internet gateways are not
// emulated. Its only network effect is publishing on the host: a TCP port
// that a security group opens to an address range (any CIDR, never 22)
// gets a forwarder on 127.0.0.1; SSH is always published.
var (
	notEnforcedInside = []string{
		"Security groups between instances (the emulator lets all traffic between instances through)",
		"Network ACLs (not emulated)",
		"Route tables, internet and NAT gateways (every instance reaches the internet through Docker)",
	}
	notEnforcedFromHost = []string{
		"Internet gateway and public IP (the emulator publishes ports on your computer instead)",
		"Route tables (not emulated)",
		"Network ACLs (not emulated)",
		"Which source addresses the security group allows (the emulator ignores the CIDR)",
	}
)

var httpPorts = []int{80, 443, 3000, 5000, 8000, 8080, 8443}

// Live runs real connections in the sandbox for a reachability request and
// labels each hop of res as verified live or checked by analysis only.
func (a *Analysis) Live(ctx context.Context, sb LiveSandbox, req model.ReachabilityRequest, res *model.ReachabilityResult) {
	lc := &model.LiveCheck{Probes: []model.LiveProbe{}, Verified: []string{}, AnalysisOnly: []string{}}
	res.Live = lc
	for i := range res.Path {
		res.Path[i].Enforcement = "analysis"
	}
	if a.basis != model.NetworkBasisDeployed {
		lc.Unavailable = "Deploy the workflow first: live tests connect to what is running in the sandbox. " + a.basisReason
		return
	}
	if strings.ToLower(req.Protocol) != "tcp" && req.Protocol != "" {
		lc.Unavailable = "Live tests open TCP connections; pick TCP."
		return
	}
	src, err := a.resolveEnd(req.Source, "source")
	if err != nil {
		lc.Unavailable = err.Error()
		return
	}
	dst, err := a.resolveEnd(req.Destination, "destination")
	if err != nil {
		lc.Unavailable = err.Error()
		return
	}
	isInstance := func(e end) bool { return e.w != nil && e.w.e.typ == "aws_instance" }
	if !(src.internet && isInstance(dst) || isInstance(src) && (dst.internet || isInstance(dst))) {
		lc.Unavailable = "Live tests cover EC2 instances: from your computer to an instance, between two instances, or from an instance to the internet. Add a test instance to try it."
		return
	}
	insts, err := sb.Instances(ctx, a.wf.ID)
	if err != nil {
		lc.Unavailable = "Couldn't list the sandbox's instances: " + err.Error()
		return
	}
	access := func(wl *workload) (sandbox.InstanceAccess, bool) {
		acc, ok := insts[wl.e.id]
		if !ok || !acc.Running {
			lc.Unavailable = fmt.Sprintf("%s (%s) isn't running in the sandbox: start the sandbox or deploy again.", wl.e.addr, wl.e.id)
			return acc, false
		}
		return acc, true
	}
	switch {
	case src.internet:
		acc, ok := access(dst.w)
		if !ok {
			return
		}
		a.liveFromHost(ctx, lc, res, dst.w, acc, req.Port)
	case dst.internet:
		acc, ok := access(src.w)
		if !ok {
			return
		}
		a.liveInside(ctx, sb, lc, res, src.w, acc, nil, req.Port)
	default:
		acc, ok := access(src.w)
		if !ok {
			return
		}
		if _, ok := access(dst.w); !ok {
			return
		}
		a.liveInside(ctx, sb, lc, res, src.w, acc, dst.w, req.Port)
	}
}

// emulatorPublishes says whether Floci publishes port on the host for wl:
// SSH always; other TCP ports when a security group opens them to a CIDR.
func emulatorPublishes(wl *workload, port int) bool {
	if port == 22 {
		return true
	}
	for _, g := range wl.sgs {
		for _, r := range g.ingress {
			if (r.proto == "tcp" || r.proto == "all") && r.ports.from <= port && port <= r.ports.to && len(r.cidrs) > 0 {
				return true
			}
		}
	}
	return false
}

func (a *Analysis) liveFromHost(ctx context.Context, lc *model.LiveCheck, res *model.ReachabilityResult, wl *workload, acc sandbox.InstanceAccess, port int) {
	predicted := emulatorPublishes(wl, port)
	probe := model.LiveProbe{Name: fmt.Sprintf("Your computer → %s port %d", wl.e.addr, port), Predicted: predicted}
	hostPort, published := acc.Ports[port]
	if !published {
		probe.Command = fmt.Sprintf("docker ps --filter name=%s-fwd-%s-%d", strings.TrimSuffix(acc.Container, "-"+acc.InstanceID), acc.InstanceID, port)
		probe.Result = "not published on your computer"
		probe.Output = fmt.Sprintf("The emulator didn't publish port %d of %s: it does that only for ports a security group opens to an address range.", port, wl.e.addr)
	} else {
		probe = hostProbe(ctx, probe, hostPort, port)
	}
	probe.Matches = probe.Connected == predicted
	lc.Probes = append(lc.Probes, probe)
	switch {
	case port == 22:
		lc.Verified = append(lc.Verified, "The SSH service answers (published on 127.0.0.1:"+strconv.Itoa(hostPort)+")")
		lc.AnalysisOnly = append(lc.AnalysisOnly, "Security groups (the emulator always publishes SSH, whatever they say)")
	default:
		lc.Verified = append(lc.Verified, fmt.Sprintf("Security group inbound rule for TCP %d (the emulator publishes a port only when a security group opens it to an address range)", port))
		if published {
			lc.Verified = append(lc.Verified, fmt.Sprintf("A service answers on port %d", port))
		}
	}
	lc.AnalysisOnly = append(lc.AnalysisOnly, notEnforcedFromHost...)
	for i := range res.Path {
		h := &res.Path[i]
		switch {
		case h.Layer == "service" && published:
			h.Enforcement = "live"
		case h.Layer == "security-group" && port != 22:
			h.Enforcement = "live"
		}
	}
	switch {
	case port == 22 && probe.Connected && !res.Reachable:
		lc.Note = "The emulator always publishes SSH on your computer, whatever the security groups say, so the live connection works; on AWS the blockers above stop it."
	case probe.Connected && !res.Reachable:
		lc.Note = "The connection worked in the sandbox because the emulator only models the security group's port; the other blockers above apply on AWS."
	case !probe.Connected && predicted && published:
		lc.Note = fmt.Sprintf("The port is published but nothing answered: the service on the instance isn't listening on port %d. On the local emulator, bind the instance's own IP (0.0.0.0:80 is taken by the metadata proxy inside every instance).", port)
	case !probe.Connected && predicted:
		lc.Note = fmt.Sprintf("A security group opens TCP %d, but the emulator hasn't published it yet: it does that 30-60 seconds after the instance starts. If you just deployed, run the live test again in a minute.", port)
	case !probe.Connected:
		lc.Note = fmt.Sprintf("As the analysis predicts, no security group opens TCP %d to an address range, so the emulator doesn't publish it.", port)
	case res.Reachable:
		lc.Note = "The live connection confirms the security group rule and the listening service; the other layers are checked by analysis."
	}
}

// hostProbe connects from this machine to 127.0.0.1:hostPort.
func hostProbe(ctx context.Context, p model.LiveProbe, hostPort, port int) model.LiveProbe {
	addr := "127.0.0.1:" + strconv.Itoa(hostPort)
	start := time.Now()
	if slices.Contains(httpPorts, port) {
		scheme := "http"
		if port == 443 || port == 8443 {
			scheme = "https"
		}
		url := scheme + "://" + addr + "/"
		p.Command = "curl -s -m 5 " + url
		if scheme == "https" {
			p.Command = "curl -sk -m 5 " + url
		}
		client := &http.Client{Timeout: 5 * time.Second,
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, // the instance's own test certificate
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		p.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			p.Result = connError(err)
			return p
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		p.Connected = true
		p.Result = "HTTP " + strconv.Itoa(resp.StatusCode)
		p.Output = strings.TrimSpace(string(body))
		return p
	}
	p.Command = "nc -z -w 5 127.0.0.1 " + strconv.Itoa(hostPort)
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	p.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		p.Result = connError(err)
		return p
	}
	// socat accepts before the upstream connects: wait for an immediate close.
	_ = conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	buf := make([]byte, 64)
	n, rerr := conn.Read(buf)
	conn.Close()
	var ne net.Error
	switch {
	case n > 0:
		p.Connected, p.Result, p.Output = true, "TCP connection opened", strings.TrimSpace(string(buf[:n]))
	case rerr == nil || errors.As(rerr, &ne) && ne.Timeout():
		p.Connected, p.Result = true, "TCP connection opened"
	default:
		p.Result = "connection closed by the instance (nothing listening)"
	}
	return p
}

func connError(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "Timeout") || strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "timed out"
	case strings.Contains(s, "EOF") || strings.Contains(s, "reset"):
		return "connection closed (nothing listening behind the published port)"
	}
	return s
}

// liveInside probes from inside the source instance's container (to dst,
// or to the internet when dst is nil).
func (a *Analysis) liveInside(ctx context.Context, sb LiveSandbox, lc *model.LiveCheck, res *model.ReachabilityResult, src *workload, acc sandbox.InstanceAccess, dst *workload, port int) {
	host, label := "example.com", "the internet (example.com)"
	if dst != nil {
		ip, _ := dst.e.str("private_ip")
		if ip == "" {
			lc.Unavailable = dst.e.addr + " has no private IP in the deployed state."
			return
		}
		host, label = ip, dst.e.addr+" ("+ip+")"
	}
	var script string
	if slices.Contains(httpPorts, port) {
		scheme := "http"
		if port == 443 || port == 8443 {
			scheme = "https"
		}
		script = fmt.Sprintf("curl -sk -m 5 -o /dev/null -w 'HTTP %%{http_code}' %s://%s:%d/", scheme, host, port)
	} else {
		script = fmt.Sprintf("if command -v nc >/dev/null 2>&1; then nc -z -w 5 %[1]s %[2]d && echo open; else timeout 5 bash -c '</dev/tcp/%[1]s/%[2]d' && echo open; fi", host, port)
	}
	probe := model.LiveProbe{Name: fmt.Sprintf("%s → %s port %d", src.e.addr, label, port), Predicted: true,
		Command: fmt.Sprintf("docker exec %s sh -c %s", acc.Container, strconv.Quote(script))}
	start := time.Now()
	out, code, err := sb.ExecInstance(ctx, a.wf.ID, acc.InstanceID, script, 12*time.Second)
	probe.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	switch {
	case err != nil:
		probe.Result = "probe failed: " + err.Error()
	case code == 0:
		probe.Connected = true
		probe.Result = strings.TrimSpace(nonEmpty(out, "connected"))
		if probe.Result == "HTTP 000" {
			probe.Connected, probe.Result = false, "no HTTP answer"
		}
	case code == 7:
		probe.Result = "connection refused (nothing listening)"
	case code == 28 || code == 124:
		probe.Result = "timed out"
	default:
		probe.Result = fmt.Sprintf("failed (exit %d)", code)
		probe.Output = out
	}
	probe.Matches = probe.Connected == probe.Predicted
	lc.Probes = append(lc.Probes, probe)
	lc.AnalysisOnly = append(lc.AnalysisOnly, notEnforcedInside...)
	if dst != nil {
		lc.Verified = append(lc.Verified, fmt.Sprintf("A service answers on %s port %d", dst.e.addr, port))
		for i := range res.Path {
			if l := res.Path[i].Layer; l == "service" || l == "endpoint" {
				res.Path[i].Enforcement = "live"
			}
		}
	} else {
		for i := range res.Path {
			if res.Path[i].Layer == "endpoint" {
				res.Path[i].Enforcement = "live"
			}
		}
	}
	switch {
	case probe.Connected && !res.Reachable:
		lc.Note = "The connection worked in the sandbox because the local emulator doesn't enforce security groups, network ACLs or route tables between instances. On AWS, the blockers above would stop it."
	case !probe.Connected && dst != nil:
		lc.Note = fmt.Sprintf("Nothing answered on %s port %d. The emulator lets all traffic between instances through, so the service isn't listening (on the emulator, bind the instance's own IP: 0.0.0.0:80 is taken by the metadata proxy).", dst.e.addr, port)
	case !probe.Connected:
		lc.Note = "The instance couldn't reach the internet from the sandbox: check your computer's internet connection."
	default:
		lc.Note = "The connection works in the sandbox; the network layers it crosses are checked by analysis, because the local emulator doesn't enforce them."
	}
}
