package sandbox

import (
	"os/exec"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Defaults applied when Config or Settings leave a field empty.
const (
	DefaultDocker         = "docker"
	DefaultTerraformImage = "hashicorp/terraform:latest"
	DefaultAWSRegion      = "us-east-1"
)

// DefaultImages returns the default emulator image of every cloud.
func DefaultImages() map[model.Cloud]string {
	images := make(map[model.Cloud]string, len(emulators))
	for c, e := range emulators {
		images[c] = e.image
	}
	return images
}

// ApplySettings replaces the docker binary, images and default AWS region
// used from now on. Running sandboxes keep their containers; the new values
// apply to sandboxes started and Terraform runs launched afterwards. Empty
// fields fall back to the defaults.
func (m *Manager) ApplySettings(s model.Settings) {
	cfg := Config{Docker: s.Docker, Images: s.Images, TerraformImage: s.TerraformImage, AWSRegion: s.DefaultAWSRegion}
	applyDefaults(&cfg)
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	m.cfg.Docker = cfg.Docker
	m.cfg.Images = cfg.Images
	m.cfg.TerraformImage = cfg.TerraformImage
	m.cfg.AWSRegion = cfg.AWSRegion
}

// ResolveDocker returns the absolute path of the docker CLI named by a
// setting: a bare command name ("docker") is looked up on PATH (which the
// daemon augments at startup with the engines' install directories), so
// every docker invocation uses the same binary. A path, or a name that is
// not found, is returned unchanged; empty means DefaultDocker.
func ResolveDocker(name string) string {
	if name == "" {
		name = DefaultDocker
	}
	if strings.ContainsAny(name, `/\`) {
		return name
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// applyDefaults fills the empty tool fields of cfg and resolves the docker
// binary; Images always becomes a fresh map holding an image for every cloud.
func applyDefaults(cfg *Config) {
	cfg.Docker = ResolveDocker(cfg.Docker)
	if cfg.TerraformImage == "" {
		cfg.TerraformImage = DefaultTerraformImage
	}
	if cfg.AWSRegion == "" {
		cfg.AWSRegion = DefaultAWSRegion
	}
	images := DefaultImages()
	for c, img := range cfg.Images {
		if _, known := images[c]; known && img != "" {
			images[c] = img
		}
	}
	cfg.Images = images
}

func (m *Manager) dockerBin() string {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.cfg.Docker
}

func (m *Manager) terraformImage() string {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.cfg.TerraformImage
}

func (m *Manager) emulatorImage(c model.Cloud) string {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.cfg.Images[c]
}

func (m *Manager) awsRegion() string {
	m.settingsMu.RLock()
	defer m.settingsMu.RUnlock()
	return m.cfg.AWSRegion
}
