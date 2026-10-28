// Package wellarch holds curated Well-Architected guidance per platform
// (AWS, Azure, GCP, OCI, Docker Compose, Kubernetes), grouped by Nimbo pillar
// and cross-referenced to the Nimbo rule codes that check each practice.
package wellarch

import "github.com/ismaelosuna7824/nimbo/backend/internal/model"

// Source is an authoritative reference for a guide.
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Practice is one actionable recommendation.
type Practice struct {
	Title string   `json:"title"`
	Why   string   `json:"why"`
	How   []string `json:"how"`
	Rules []string `json:"rules,omitempty"`
}

// PillarGuide groups the practices of one pillar.
type PillarGuide struct {
	Pillar    model.Pillar `json:"pillar"`
	Summary   string       `json:"summary"`
	Practices []Practice   `json:"practices"`
}

// Guide is the full guidance for one platform.
type Guide struct {
	Platform  string        `json:"platform"`
	Name      string        `json:"name"`
	Framework string        `json:"framework"`
	Sources   []Source      `json:"sources"`
	Pillars   []PillarGuide `json:"pillars"`
}

var platforms = []string{"aws", "azure", "gcp", "oci", "docker", "kubernetes"}

var guides = map[string]func() Guide{
	"aws":        awsGuide,
	"azure":      azureGuide,
	"gcp":        gcpGuide,
	"oci":        ociGuide,
	"docker":     dockerGuide,
	"kubernetes": kubernetesGuide,
}

// Platforms lists the supported platform keys in display order.
func Platforms() []string {
	return append([]string(nil), platforms...)
}

// Get returns the guide for a platform key. A fresh copy is built on every
// call so callers may mutate it freely.
func Get(platform string) (Guide, bool) {
	fn, ok := guides[platform]
	if !ok {
		return Guide{}, false
	}
	return fn(), true
}

// p builds a Practice; trailing variadic arguments are rule codes.
func p(title, why string, how []string, rules ...string) Practice {
	return Practice{Title: title, Why: why, How: how, Rules: rules}
}

func pillar(pl model.Pillar, summary string, practices ...Practice) PillarGuide {
	return PillarGuide{Pillar: pl, Summary: summary, Practices: practices}
}
