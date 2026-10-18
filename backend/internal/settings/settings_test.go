package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestOpenMissingFileYieldsDefaults(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := st.Get()
	if got.DefaultMode != ModeGuided || got.Docker != "docker" || got.TerraformImage != "hashicorp/terraform:latest" ||
		got.DefaultAWSRegion != "us-east-1" || got.Images[model.CloudAWS] != "floci/floci:latest" || len(got.Images) != 4 {
		t.Fatalf("defaults = %+v", got)
	}
}

func TestPutPersistsAndFillsDefaults(t *testing.T) {
	home := t.TempDir()
	st, _ := Open(home)
	saved, err := st.Put(model.Settings{
		DefaultMode:      "pro",
		Images:           map[model.Cloud]string{model.CloudAWS: " ghcr.io/me/floci:2.3.0 ", model.CloudGCP: ""},
		DefaultAWSRegion: "eu-west-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Images[model.CloudAWS] != "ghcr.io/me/floci:2.3.0" || saved.Images[model.CloudGCP] != "floci/floci-gcp:latest" ||
		saved.Docker != "docker" || saved.TerraformImage != "hashicorp/terraform:latest" {
		t.Fatalf("saved = %+v", saved)
	}
	info, err := os.Stat(filepath.Join(home, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
	again, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	got := again.Get()
	if got.DefaultMode != "pro" || got.DefaultAWSRegion != "eu-west-1" || got.Images[model.CloudAWS] != "ghcr.io/me/floci:2.3.0" {
		t.Fatalf("reloaded = %+v", got)
	}
	// Get returns a copy.
	got.Images[model.CloudAWS] = "x"
	if again.Get().Images[model.CloudAWS] == "x" {
		t.Error("Get exposes internal map")
	}
}

func TestPutRejectsInvalid(t *testing.T) {
	st, _ := Open(t.TempDir())
	for name, s := range map[string]model.Settings{
		"mode":          {DefaultMode: "expert"},
		"image":         {Images: map[model.Cloud]string{model.CloudAWS: "Floci Latest"}},
		"unknown cloud": {Images: map[model.Cloud]string{"ibm": "x/y"}},
		"tf image":      {TerraformImage: "hashicorp/terraform:"},
		"region":        {DefaultAWSRegion: "Europe"},
		"relative bin":  {Docker: "bin/docker"},
	} {
		if _, err := st.Put(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if st.Get().DefaultMode != ModeGuided {
		t.Error("rejected Put changed the settings")
	}
	for _, ok := range []model.Settings{
		{Docker: "/usr/local/bin/podman"},
		{TerraformImage: "registry.local:5000/terraform@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{DefaultAWSRegion: "ap-southeast-2"},
		{DefaultAWSRegion: "us-gov-west-1"},
	} {
		if _, err := st.Put(ok); err != nil {
			t.Errorf("Put(%+v) = %v", ok, err)
		}
	}
}

func TestOpenKeepsValidFieldsOfBadFile(t *testing.T) {
	home := t.TempDir()
	data := `{"defaultMode":"pro","docker":"","images":{"aws":"BAD IMAGE","azure":"me/az:1"},"terraformImage":"tf:1.9","defaultAWSRegion":"nowhere"}`
	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	got := st.Get()
	if got.DefaultMode != "pro" || got.TerraformImage != "tf:1.9" || got.Images[model.CloudAzure] != "me/az:1" ||
		got.Images[model.CloudAWS] != "floci/floci:latest" || got.DefaultAWSRegion != "us-east-1" {
		t.Fatalf("got %+v", got)
	}

	if err := os.WriteFile(filepath.Join(home, "settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err = Open(home)
	if err != nil || st.Get().DefaultMode != ModeGuided {
		t.Fatalf("unparsable file: %v %+v", err, st)
	}
}
