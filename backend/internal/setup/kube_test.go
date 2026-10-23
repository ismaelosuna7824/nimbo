package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

func TestKubeCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker is a shell script")
	}
	// A fake docker answering `image inspect` when NIMBO_FAKE_IMAGE=1.
	bin := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\nif [ \"$NIMBO_FAKE_IMAGE\" = 1 ]; then echo 248355420; exit 0; fi\necho 'Error: No such image: x' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Checker{cfg: Config{Home: filepath.Join(t.TempDir(), "missing", "nimbo")}, plat: Platform{GOOS: "darwin"}}
	cli := cliInfo{bin: bin}
	info := &dockerInfo{ServerVersion: "29.0"}
	ctx := context.Background()

	t.Setenv("NIMBO_FAKE_IMAGE", "0")
	missing := c.kubeCheck(ctx, cli, info)
	if missing.ID != CheckKubernetes || missing.Required || missing.Status != model.SetupMissing || len(missing.Fixes) != 1 ||
		missing.Fixes[0].Action != model.SetupActionPullKubeImage || missing.Fixes[0].Command != "docker pull "+sandbox.KubeNodeImage {
		t.Errorf("missing = %+v", missing)
	}
	if !strings.Contains(missing.Detail, "~/.kube/config is never changed") {
		t.Errorf("detail = %q", missing.Detail)
	}

	t.Setenv("NIMBO_FAKE_IMAGE", "1")
	ok := c.kubeCheck(ctx, cli, info)
	if ok.Status != model.SetupOK || ok.Summary != "k3s v1.36.5-k3s1 (≈ 236 MB)" {
		t.Errorf("ok = %+v", ok)
	}

	pending := c.kubeCheck(ctx, cliInfo{err: errors.New("not found")}, nil)
	if pending.Status != model.SetupMissing || len(pending.Fixes) != 0 {
		t.Errorf("pending = %+v", pending)
	}
}
