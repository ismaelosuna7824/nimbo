package endpoints

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

const iid = "i-e159e1fad6152831b"

func instanceFixture(instances map[string]sandbox.InstanceAccess) (*model.Workflow, *model.DeployedState, Sandbox) {
	wf := &model.Workflow{ID: "wf1"}
	st := &model.DeployedState{WorkflowID: "wf1", Resources: []model.DeployedResource{
		res("aws_instance.web", map[string]any{
			"id": iid, "arn": "arn:aws:ec2:us-east-1:000000000000:instance/" + iid,
			"private_ip": "10.0.1.10", "public_ip": "10.0.1.10", "availability_zone": "us-east-1a",
			"subnet_id": "subnet-1", "key_name": "deployer",
		}),
	}}
	sb := Sandbox{Bases: map[model.Cloud]string{model.CloudAWS: base}, Running: map[model.Cloud]bool{model.CloudAWS: true}, Instances: instances}
	return wf, st, sb
}

func TestInstanceAccess(t *testing.T) {
	container := "floci-aws-nimbo-wf1-ec2-" + iid
	for _, tc := range []struct {
		name      string
		instances map[string]sandbox.InstanceAccess
		hostURL   string // Host URL (port 80)
		ssh       string
		endpoint  string
		action    bool
		warning   string
	}{
		{
			name: "published port 80 and SSH",
			instances: map[string]sandbox.InstanceAccess{iid: {
				InstanceID: iid, Container: container, Running: true, Ports: map[int]int{22: 2201, 80: 30000, 9000: 30001},
			}},
			hostURL: "http://127.0.0.1:30000/", ssh: "127.0.0.1:2201", endpoint: "http://127.0.0.1:30000/", action: true,
		},
		{
			name: "only SSH",
			instances: map[string]sandbox.InstanceAccess{iid: {
				InstanceID: iid, Container: container, Running: true, Ports: map[int]int{22: 2201},
			}},
			ssh: "127.0.0.1:2201", warning: instanceNoPorts,
		},
		{name: "sandbox stopped", warning: instanceNotStarted},
		{
			name:      "container stopped",
			instances: map[string]sandbox.InstanceAccess{iid: {InstanceID: iid, Container: container, Ports: map[int]int{}}},
			warning:   instanceStopped,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf, st, sb := instanceFixture(tc.instances)
			r := byAddr(t, Build(wf, st, sb), "aws_instance.web")
			if r.Kind != KindInstance || r.Supporting || r.Summary != instanceSummary {
				t.Fatalf("kind/summary: %+v", r)
			}
			for label, want := range map[string]string{
				"Instance ID": iid, "Private IP": "10.0.1.10", "Public IP": "10.0.1.10", "Availability zone": "us-east-1a",
				"Subnet": "subnet-1", "Host URL (port 80)": tc.hostURL, "SSH": tc.ssh,
			} {
				if got := field(r, label); got != want {
					t.Errorf("field %s = %q, want %q", label, got, want)
				}
			}
			if r.Warning != tc.warning {
				t.Errorf("warning = %q, want %q", r.Warning, tc.warning)
			}
			if (hasAction(r, ActRequest) != nil) != tc.action {
				t.Errorf("request action = %v, want %v", r.Actions, tc.action)
			}
			if tc.endpoint == "" {
				if len(r.Endpoints) != 0 {
					t.Errorf("endpoints = %+v", r.Endpoints)
				}
				return
			}
			// Port 9000 is published (Host URL) but not a usual HTTP port.
			if len(r.Endpoints) != 1 || r.Endpoints[0].URL != tc.endpoint || r.Endpoints[0].Method != http.MethodGet {
				t.Errorf("endpoints = %+v", r.Endpoints)
			}
			if field(r, "Host URL (port 9000)") != "http://127.0.0.1:30001/" {
				t.Errorf("fields = %+v", r.Fields)
			}
			if c := snippet(t, r, "curl"); !strings.Contains(c, "curl -i http://127.0.0.1:30000/") || !strings.Contains(c, "http://127.0.0.1:30001/") {
				t.Errorf("curl = %q", c)
			}
			if s := snippet(t, r, "Open shell"); s != "docker exec -it "+container+" sh" {
				t.Errorf("shell = %q", s)
			}
		})
	}
}

func TestInstanceTry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello from instance %s", r.URL.Path)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	wf, st, sb := instanceFixture(map[string]sandbox.InstanceAccess{iid: {
		InstanceID: iid, Container: "c", Running: true, Ports: map[int]int{22: 1, 80: port},
	}})
	ctx := context.Background()
	// The sandbox's AWS emulator need not run for the instance's own port.
	sb.Running = map[model.Cloud]bool{}

	res, err := Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_instance.web", Action: ActRequest})
	if err != nil || !res.OK || res.Body != "hello from instance /" {
		t.Fatalf("default path: %+v %v", res, err)
	}
	res, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_instance.web", Action: ActRequest, Path: "/health"})
	if err != nil || !res.OK || res.Body != "hello from instance /health" {
		t.Fatalf("relative path: %+v %v", res, err)
	}
	// The instance's published port is allowed from other resources too.
	res, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_instance.web", Action: ActRequest, Path: "http://localhost:" + u.Port() + "/x"})
	if err != nil || !res.OK {
		t.Fatalf("full URL: %+v %v", res, err)
	}
	var re *RequestError
	for _, path := range []string{"http://127.0.0.1:1/", "http://127.0.0.1:31999/", "http://10.0.1.10/"} {
		if _, err := Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_instance.web", Action: ActRequest, Path: path}); !errors.As(err, &re) {
			t.Errorf("%s: want a RequestError, got %v", path, err)
		}
	}

	stopped := sb
	stopped.Instances = map[string]sandbox.InstanceAccess{iid: {InstanceID: iid, Ports: map[int]int{80: port}}}
	if _, err := Try(ctx, wf, st, stopped, model.TryRequest{NodeID: "aws_instance.web", Action: ActRequest}); err == nil {
		t.Fatalf("stopped instance has no request action, want an error")
	}

	col := Postman(wf, st, sb)
	var folder *PostmanItem
	for i := range col.Item {
		if col.Item[i].Name == "EC2 instances (sandbox)" {
			folder = &col.Item[i]
		}
	}
	if folder == nil || len(folder.Item) != 1 || folder.Item[0].Request.URL.Raw != "http://127.0.0.1:"+u.Port()+"/" || folder.Item[0].Request.URL.Port != u.Port() {
		t.Fatalf("postman folder: %+v", folder)
	}
}
