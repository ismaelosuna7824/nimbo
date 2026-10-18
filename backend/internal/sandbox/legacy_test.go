package sandbox

import "testing"

func TestLegacyContainerOwned(t *testing.T) {
	wfs := map[string]bool{"wf-1": true}
	cases := []struct {
		name string
		c    container
		want bool
	}{
		{"workflow label", container{Names: "whatever", Labels: "io.iaccloud.workflow=wf-1,io.iaccloud.role=pod"}, true},
		{"workflow label other wf", container{Names: "iacc-wf-2-pod", Labels: "io.iaccloud.workflow=wf-2"}, false},
		{"floci namespace", container{Names: "floci-lambda-x", Labels: "floci_namespace=iacc-wf-1"}, true},
		{"floci namespace other", container{Names: "floci-lambda-x", Labels: "floci_namespace=iacc-wf-9"}, false},
		{"pod name", container{Names: "iacc-wf-1-pod"}, true},
		{"emulator name", container{Names: "iacc-wf-1-azure"}, true},
		{"tf name", container{Names: "iacc-wf-1-tf-r1-apply"}, true},
		{"unrelated suffix", container{Names: "iacc-wf-1-db"}, false},
		{"unknown wf", container{Names: "iacc-wf-2-aws"}, false},
		{"new naming", container{Names: "nimbo-wf-1-pod", Labels: "io.nimbo.workflow=wf-1"}, false},
	}
	for _, tc := range cases {
		if got := legacyContainerOwned(tc.c, wfs); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLegacyNetworkOwned(t *testing.T) {
	wfs := map[string]bool{"wf-1": true}
	cases := []struct {
		name, labels string
		want         bool
	}{
		{"iacc-wf-1", "", true},
		{"iacc-wf-2", "", false},
		{"floci-net", "floci_namespace=iacc-wf-1", true},
		{"floci-net", "floci_namespace=iacc-wf-2", false},
		{"nimbo-wf-1", "", false},
		{"bridge", "", false},
	}
	for _, tc := range cases {
		if got := legacyNetworkOwned(tc.name, tc.labels, wfs); got != tc.want {
			t.Errorf("%s %q: got %v, want %v", tc.name, tc.labels, got, tc.want)
		}
	}
}
