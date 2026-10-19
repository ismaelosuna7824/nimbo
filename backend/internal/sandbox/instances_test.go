package sandbox

import (
	"maps"
	"strings"
	"testing"
)

func TestParsePsPorts(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want map[int]int
	}{
		{"", map[int]int{}},
		{"0.0.0.0:30000->80/tcp, :::30000->80/tcp", map[int]int{80: 30000}},
		{"0.0.0.0:2201->22/tcp, [::]:2201->22/tcp", map[int]int{22: 2201}},
		{"127.0.0.1:45000->4566/tcp", map[int]int{4566: 45000}},
		{"80/tcp, 443/tcp", map[int]int{}},
		{"0.0.0.0:30001->53/udp", map[int]int{}},
		{"192.168.1.5:30002->8080/tcp", map[int]int{}},
		{"0.0.0.0:30010-30011->8000-8001/tcp", map[int]int{8000: 30010, 8001: 30011}},
		{"0.0.0.0:30010-30012->8000-8001/tcp", map[int]int{}},
		{"0.0.0.0:2202->22/tcp, 0.0.0.0:30003->3000/tcp", map[int]int{22: 2202, 3000: 30003}},
	} {
		if got := parsePsPorts(tc.in); !maps.Equal(got, tc.want) {
			t.Errorf("parsePsPorts(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseInstances(t *testing.T) {
	prefix := instancePrefix("wf-1")
	if prefix != "floci-aws-nimbo-wf-1-ec2-" {
		t.Fatalf("prefix = %q", prefix)
	}
	lines := []string{
		prefix + "i-e159e1fad6152831b\trunning\t0.0.0.0:2201->22/tcp, :::2201->22/tcp",
		prefix + "fwd-i-e159e1fad6152831b-80\trunning\t0.0.0.0:30000->80/tcp, :::30000->80/tcp",
		prefix + "fwd-i-e159e1fad6152831b-8080\texited\t",
		prefix + "fwd-i-e159e1fad6152831b-3000\trunning\t0.0.0.0:30001->3000/tcp",
		prefix + "i-0abc\texited\t",
		// Sidecar of an instance whose container is gone.
		prefix + "fwd-i-0def-80\trunning\t0.0.0.0:30002->80/tcp",
		// Another workflow whose ID starts with "wf-1-ec2-".
		prefix + "i-1-ec2-i-0aaa\trunning\t0.0.0.0:2203->22/tcp",
		// Unrelated container.
		"floci-aws-nimbo-wf-2-ec2-i-0bbb\trunning\t0.0.0.0:2204->22/tcp",
		"",
	}
	got := parseInstances(prefix, strings.Join(lines, "\n"))
	want := map[string]InstanceAccess{
		"i-e159e1fad6152831b": {
			InstanceID: "i-e159e1fad6152831b", Container: prefix + "i-e159e1fad6152831b", Running: true,
			Ports: map[int]int{22: 2201, 80: 30000, 3000: 30001},
		},
		"i-0abc": {InstanceID: "i-0abc", Container: prefix + "i-0abc", Ports: map[int]int{}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d instances: %+v", len(got), got)
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok || g.InstanceID != w.InstanceID || g.Container != w.Container || g.Running != w.Running || !maps.Equal(g.Ports, w.Ports) {
			t.Errorf("%s = %+v, want %+v", id, g, w)
		}
	}
	if got := parseInstances(prefix, ""); got == nil || len(got) != 0 {
		t.Errorf("empty output = %#v", got)
	}
}

func TestExecFailedAndCap(t *testing.T) {
	for out, want := range map[string]bool{
		"Error response from daemon: No such container: x":         true,
		"Error response from daemon: container abc is not running": true,
		"Error: No such container: x":                              true,
		"curl: (7) Failed to connect":                              false,
		"":                                                         false,
	} {
		if got := execFailed(out); got != want {
			t.Errorf("execFailed(%q) = %v", out, got)
		}
	}
	long := strings.Repeat("a", maxExecOutput+100)
	if got := capOutput("  " + long + "\n"); !strings.HasPrefix(got, long[:maxExecOutput]) || !strings.HasSuffix(got, "(output truncated)") || len(got) > maxExecOutput+64 {
		t.Errorf("capOutput length %d", len(got))
	}
	if got := capOutput(" ok \n"); got != "ok" {
		t.Errorf("capOutput = %q", got)
	}
}
