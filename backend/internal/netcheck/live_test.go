package netcheck

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

type fakeSandbox struct {
	insts  map[string]sandbox.InstanceAccess
	script string
	out    string
	code   int
}

func (f *fakeSandbox) Instances(context.Context, string) (map[string]sandbox.InstanceAccess, error) {
	return f.insts, nil
}

func (f *fakeSandbox) ExecInstance(_ context.Context, _, _, script string, _ time.Duration) (string, int, error) {
	f.script = script
	return f.out, f.code, nil
}

func deployedFixture(t *testing.T) (*model.Workflow, *Analysis) {
	t.Helper()
	wf := importDir(t, "testdata/deployed/aws-vpc-basics", nil)
	raw, err := os.ReadFile("testdata/deployed/aws-vpc-basics.show.json")
	if err != nil {
		t.Fatal(err)
	}
	st, err := sandbox.ParseState(raw, wf)
	if err != nil {
		t.Fatal(err)
	}
	return wf, Analyze(wf, st)
}

func TestLiveFromHost(t *testing.T) {
	wf, a := deployedFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "hello from web") }))
	defer srv.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	var hostPort int
	fmt.Sscan(portStr, &hostPort)
	iid := ""
	for _, e := range a.aws.ents {
		if e.typ == "aws_instance" {
			iid = e.id
		}
	}
	sb := &fakeSandbox{insts: map[string]sandbox.InstanceAccess{iid: {InstanceID: iid, Container: "floci-aws-nimbo-wf-ec2-" + iid, Running: true, Ports: map[int]int{80: hostPort}}}}

	req := model.ReachabilityRequest{Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: nodeID(t, wf, "aws_instance.web")}, Protocol: "tcp", Port: 80, Live: true}
	res, err := a.Reachability(req)
	if err != nil {
		t.Fatal(err)
	}
	a.Live(context.Background(), sb, req, &res)
	lc := res.Live
	if lc == nil || lc.Unavailable != "" || len(lc.Probes) != 1 {
		t.Fatalf("live %+v", lc)
	}
	p := lc.Probes[0]
	if !p.Connected || !p.Predicted || !p.Matches || p.Result != "HTTP 200" || p.Output != "hello from web" || !strings.Contains(p.Command, fmt.Sprintf("127.0.0.1:%d", hostPort)) {
		t.Errorf("probe %+v", p)
	}
	enf := map[string]string{}
	for _, h := range res.Path {
		enf[h.Layer] = h.Enforcement
	}
	if enf["security-group"] != "live" || enf["route"] != "analysis" || enf["nacl"] != "analysis" || enf["gateway"] != "analysis" {
		t.Errorf("enforcement %v", enf)
	}
	if len(lc.AnalysisOnly) == 0 || len(lc.Verified) == 0 {
		t.Errorf("layers %+v", lc)
	}

	// A port no security group opens is not published: blocked, as predicted.
	req.Port = 8080
	res, _ = a.Reachability(req)
	a.Live(context.Background(), sb, req, &res)
	p = res.Live.Probes[0]
	if p.Connected || p.Predicted || !p.Matches || res.Reachable || !strings.Contains(p.Result, "not published") {
		t.Errorf("8080 probe %+v", p)
	}

	// From the instance to the internet: runs inside its container.
	sb.out = "HTTP 200"
	req = model.ReachabilityRequest{Source: model.NetEndpoint{Kind: "node", NodeID: "aws_instance.web"}, Destination: model.NetEndpoint{Kind: "internet"}, Protocol: "tcp", Port: 443, Live: true}
	res, _ = a.Reachability(req)
	a.Live(context.Background(), sb, req, &res)
	p = res.Live.Probes[0]
	if !p.Connected || !strings.Contains(sb.script, "https://example.com:443/") || !strings.Contains(p.Command, "docker exec floci-aws-nimbo-wf-ec2-") {
		t.Errorf("egress probe %+v (script %q)", p, sb.script)
	}

	// Not running: unavailable, analysis still there.
	sb.insts = map[string]sandbox.InstanceAccess{}
	res, _ = a.Reachability(req)
	a.Live(context.Background(), sb, req, &res)
	if !strings.Contains(res.Live.Unavailable, "isn't running") || len(res.Path) == 0 {
		t.Errorf("not running %+v", res.Live)
	}
}
