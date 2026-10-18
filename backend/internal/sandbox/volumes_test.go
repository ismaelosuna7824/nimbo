package sandbox

import (
	"slices"
	"strings"
	"testing"
)

// Docker outputs as seen for a floci-oci sandbox with a function and an
// OKE cluster (floci-oci 0.5.0), plus volumes of other sandboxes.
func TestSandboxVolumes(t *testing.T) {
	ns := networkName("shop-ab12cd")
	const cluster = "ocid1.cluster.oc1.iad.ma6tdxzuktmyfgvemqfajceo346elzb6q75wfxiqp3nm6e6jzofsx456kldy"
	labeled := strings.Fields(`
floci-oci-nimbo-shop-ab12cd-fnserver-iofs
floci-gcp-nimbo-shop-ab12cd-gke-floci-local-app-gke
`)
	// docker inspect of the spawned containers (fnserver, k3s, Lambda).
	mounted := strings.Fields(`
floci-oci-nimbo-shop-ab12cd-fnserver-iofs
floci-oci-nimbo-shop-ab12cd-oke-vol-` + cluster + `
nimbo-terraform-plugin-cache
`)
	// docker volume ls --filter name=floci-oci-nimbo-shop-ab12cd-oke-vol-
	// (a substring filter: a longer namespace shows up too).
	named := strings.Fields(`
floci-oci-nimbo-shop-ab12cd-oke-vol-` + cluster + `
floci-oci-nimbo-shop-ab12cd-oke-vol-ocid1.cluster.oc1.iad.deleted
floci-oci-nimbo-shop-ab12cd-x-oke-vol-ocid1.cluster.oc1.iad.other
`)
	got := sandboxVolumes(ns, labeled, mounted, named)
	want := []string{
		"floci-gcp-nimbo-shop-ab12cd-gke-floci-local-app-gke",
		"floci-oci-nimbo-shop-ab12cd-fnserver-iofs",
		"floci-oci-nimbo-shop-ab12cd-oke-vol-ocid1.cluster.oc1.iad.deleted",
		"floci-oci-nimbo-shop-ab12cd-oke-vol-" + cluster,
	}
	if !slices.Equal(got, want) {
		t.Errorf("sandboxVolumes:\n got %q\nwant %q", got, want)
	}
}

func TestSandboxVolumesLeavesOthers(t *testing.T) {
	ns := networkName("shop")
	got := sandboxVolumes(ns, nil,
		// Mounted by our containers but not named after the sandbox: shared.
		[]string{"nimbo-terraform-plugin-cache", "floci-oci-nimbo-shopx-oke-vol-ocid1.cluster.oc1.iad.a"},
		// Another sandbox whose ID extends ours, and a non-cluster suffix.
		[]string{"floci-oci-nimbo-shop-2-oke-vol-ocid1.cluster.oc1.iad.b", "floci-oci-nimbo-shop-oke-vol-backup"})
	if len(got) != 0 {
		t.Errorf("attributed foreign volumes: %q", got)
	}
}
