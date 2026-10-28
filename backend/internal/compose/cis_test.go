package compose

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// cisFindings lists the findings of the CIS rules as severity:code@node:line.
func cisFindings(p *Project) []string {
	var out []string
	for _, d := range p.Diagnostics {
		if slices.ContainsFunc(cisRules, func(r Rule) bool { return r.Code == d.Code }) {
			out = append(out, string(d.Severity)+":"+d.Code+"@"+d.NodeID+":"+itoa(d.Line))
		}
	}
	slices.Sort(out)
	return out
}

func TestCISServiceRules(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `services:
  bad:
    image: alpine:3.20
    cap_add: [SYS_ADMIN, NET_RAW]
    security_opt:
      - seccomp:unconfined
      - apparmor=unconfined
      - label=disable
    ipc: host
    uts: host
    userns_mode: host
    network_mode: bridge
    devices:
      - /dev/sda:/dev/sda
    ports:
      - "80:80"
    volumes:
      - /etc:/host/etc
      - /proc:/host/proc:ro
      - /usr/share/zoneinfo:/zoneinfo:ro
      - /srv/shared:/shared:shared
  good:
    image: alpine:3.20
    read_only: true
    cap_drop: [ALL]
    cap_add: [NET_BIND_SERVICE]
    security_opt: ["no-new-privileges:true"]
    pids_limit: 100
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - /etc/localtime:/etc/localtime:ro
      - ./data:/data
`,
		"data/.keep": "",
	})
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	want := []string{
		"info:dk_default_bridge@service:bad:12",
		"info:dk_devices@service:bad:14",
		"info:dk_host_dirs@service:bad:19", // /proc read-only
		"info:dk_net_raw@service:bad:4",
		"info:dk_no_new_privileges@service:bad:6",
		"info:dk_pids_limit@service:bad:2",
		"info:dk_port_interface@service:bad:16",
		"info:dk_privileged_port@service:bad:16",
		"info:dk_read_only@service:bad:2",
		"warning:dk_apparmor_unconfined@service:bad:7",
		"warning:dk_cap_add@service:bad:4",
		"warning:dk_host_dirs@service:bad:18", // /etc read-write
		"warning:dk_host_ipc@service:bad:9",
		"warning:dk_host_userns@service:bad:11",
		"warning:dk_host_uts@service:bad:10",
		"warning:dk_mount_propagation@service:bad:21",
		"warning:dk_seccomp_unconfined@service:bad:6",
		"warning:dk_selinux_disabled@service:bad:8",
	}
	slices.Sort(want)
	if got := cisFindings(p); !slices.Equal(got, want) {
		t.Errorf("findings:\n got %v\nwant %v", got, want)
	}
}

func TestCISNetRawDropped(t *testing.T) {
	p := open(t, map[string]string{"compose.yaml": `services:
  raw:
    image: alpine:3.20
    cap_drop: [NET_RAW]
  privileged:
    image: alpine:3.20
    cap_drop: [ALL]
    privileged: true
`})
	got := cisFindings(p)
	if slices.ContainsFunc(got, func(f string) bool { return strings.Contains(f, "dk_net_raw@service:raw") }) {
		t.Errorf("cap_drop NET_RAW still flagged: %v", got)
	}
	if !slices.Contains(got, "info:dk_net_raw@service:privileged:8") {
		t.Errorf("privileged container keeps NET_RAW: %v", got)
	}
}

func TestCISDockerfileRules(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `services:
  web:
    build: ./web
    user: "1000"
  ok:
    build: ./ok
    user: "1000"
`,
		"web/Dockerfile": `FROM debian:12-slim
RUN apt-get update
RUN apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*
ADD app.tar.gz /app/
`,
		"ok/Dockerfile": `FROM alpine:3.20
RUN apk update && apk add --no-cache curl
COPY . /app
`,
	})
	var got []string
	for _, f := range cisFindings(p) {
		if strings.Contains(f, "dk_add_any") || strings.Contains(f, "dk_update_alone") {
			got = append(got, f)
		}
	}
	want := []string{"info:dk_add_any@service:web:4", "info:dk_update_alone@service:web:2"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestUpdateAlone(t *testing.T) {
	cases := map[string]bool{
		"apt-get update":                            true,
		"apt-get update && apt-get upgrade -y":      true,
		"yum -y update":                             true,
		"apt-get update && apt-get install -y curl": false,
		"apk update && apk add curl":                false,
		"dnf makecache; dnf install -y git":         false,
		"npm update":                                false,
		"pip install --upgrade pip":                 false,
	}
	for run, alone := range cases {
		if got := updateAlone(segments(run)) != ""; got != alone {
			t.Errorf("%q: alone %v, want %v", run, got, alone)
		}
	}
}

func TestCISRulesRegistered(t *testing.T) {
	quiet := map[string]bool{"dk_net_raw": true, "dk_read_only": true, "dk_no_new_privileges": true, "dk_pids_limit": true,
		"dk_port_interface": true, "dk_privileged_port": true, "dk_add_any": true}
	for _, r := range cisRules {
		got, ok := ruleByCode[r.Code]
		if !ok || !slices.ContainsFunc(Rules(), func(b model.BestPracticeRule) bool { return b.Code == r.Code }) {
			t.Errorf("%s is not registered", r.Code)
		}
		if got.DefaultOff != quiet[r.Code] {
			t.Errorf("%s: opt-in %v", r.Code, got.DefaultOff)
		}
		if r.Docs == "" || r.Why == "" || r.Description == "" {
			t.Errorf("%s lacks docs, why or description", r.Code)
		}
	}
}
