package compliance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestAttestationsPersist(t *testing.T) {
	home := t.TempDir()
	if att, err := LoadAttestations(home, "wf-1"); err != nil || len(att) != 0 {
		t.Fatalf("no file: %v %v", att, err)
	}
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	key := Key("cis-aws-7.0", "1.5")
	if err := Attest(home, "wf-1", key, Attestation{Note: "  root MFA on  ", By: "ana", At: at}); err != nil {
		t.Fatal(err)
	}
	if err := Attest(home, "wf-1", Key("cis-aws-7.0", "1.6"), Attestation{Note: "hardware key", At: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "workflows", "wf-1", "compliance.json")); err != nil {
		t.Fatal(err)
	}
	att, err := LoadAttestations(home, "wf-1")
	if err != nil || len(att) != 2 || att[key].Note != "root MFA on" || att[key].By != "ana" || !att[key].At.Equal(at) {
		t.Fatalf("loaded %+v %v", att, err)
	}
	if err := Unattest(home, "wf-1", key); err != nil {
		t.Fatal(err)
	}
	if err := Unattest(home, "wf-1", key); err != nil {
		t.Fatalf("removing a missing attestation: %v", err)
	}
	if att, _ := LoadAttestations(home, "wf-1"); len(att) != 1 {
		t.Errorf("after removal: %+v", att)
	}
	if err := Attest(home, "../x", key, Attestation{}); !errors.Is(err, ErrInvalidID) {
		t.Errorf("unsafe id: %v", err)
	}
}

func TestMarkdownReport(t *testing.T) {
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	att := Attestations{Key(testFramework.ID, "3.1"): {Note: "MFA | hardware key", By: "ana", At: at}}
	diags := []model.Diagnostic{diag("bp_test_enc", "n-logs"), diag("bp_test_public", "n-site")}
	r := Evaluate(testFramework, tfWorkflow(), diags, model.BestPracticeSettings{}, att)
	md := Markdown("Shop API", r, at)
	for _, want := range []string{
		"# Compliance pre-audit: Test AWS",
		"- **Workflow:** Shop API",
		"v1.0.0 (`test-aws-1.0`)",
		"- **Date:** 2026-10-02",
		"not a certification",
		"**1 / 3 automatic controls met.**",
		"| Violated | 2 |",
		"| 1.1 Buckets encrypted | Violated | `bp_test_enc` | 1 of 2 resources have findings.<br>aws_s3_bucket.logs: bp_test_enc on n-logs Fix: fix it |",
		"## Storage",
		"| 3.1 Root MFA | Attested | manual | Attested: MFA \\| hardware key |",
		"## Attestations",
		"| 3.1 Root MFA | MFA \\| hardware key | ana | 2026-10-02 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report misses %q:\n%s", want, md)
		}
	}
	// Within a section, status order beats benchmark order: met (3.2) before attested (3.1).
	if strings.Index(md, "| 3.2 Design-wide rule | Met") > strings.Index(md, "| 3.1 Root MFA | Attested") {
		t.Errorf("status order not kept:\n%s", md)
	}
}
