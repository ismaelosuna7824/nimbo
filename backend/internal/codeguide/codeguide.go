// Package codeguide is Nimbo's application-code guidance for agents: how to
// write the code a workflow deploys, in two modes. Architecture mode ships
// only smoke handlers that prove the wiring (health plus a write/read round
// trip per connected store); full mode ships a hexagonal skeleton (domain,
// use cases, ports, adapters, composition root, tests) scaled to the chosen
// style. Code always lives in workflow asset folders services/<svc>/ and
// specs in openspec/, never inline in Terraform; services that need a build
// have a Dockerfile whose last stage, "package", holds the deployable files
// (Nimbo builds it into services/<svc>/dist before Terraform runs).
package codeguide

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Modes, styles, platforms and targets accepted by Get.
const (
	ModeArchitecture = model.ModeArchitecture
	ModeFull         = model.ModeFull

	StyleService         = "service"
	StyleModularMonolith = "modular-monolith"
	StyleMicroservices   = "microservices"

	TargetFunction  = "function"
	TargetContainer = "container"
	TargetVM        = "vm"
)

var (
	Modes     = []string{ModeArchitecture, ModeFull}
	Languages = []string{"typescript", "javascript", "python", "go", "java", "csharp"}
	Styles    = []string{StyleService, StyleModularMonolith, StyleMicroservices}
	Platforms = []string{"aws", "azure", "gcp", "oci", "docker", "kubernetes"}
	Targets   = []string{TargetFunction, TargetContainer, TargetVM}
)

// Options selects the guidance. Empty fields take defaults: mode
// architecture, style service, platform aws, target function on clouds and
// container on docker/kubernetes; an empty language in full mode returns
// the language options to propose instead of files.
type Options struct {
	Mode     string `json:"mode,omitempty"`
	Language string `json:"language,omitempty"`
	Style    string `json:"style,omitempty"`
	Platform string `json:"platform,omitempty"`
	Target   string `json:"target,omitempty"`
}

type Folder struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose"`
}

type File struct {
	Path    string `json:"path"`
	Purpose string `json:"purpose"`
	Content string `json:"content"`
}

type LanguageOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Why  string `json:"why"`
	When string `json:"when"`
}

// Packaging tells how the service reaches the platform. Kind: zip (source
// folder zipped by Terraform), build-zip (package stage built into dist,
// then zipped), image (container image pushed to a registry),
// compose-build or k8s-image (built locally by Nimbo), vm-image (a VM runs
// the image under systemd). Terraform is the snippet wiring it (empty for
// docker/kubernetes, whose snippet is in How).
type Packaging struct {
	Kind      string `json:"kind"`
	How       string `json:"how"`
	Terraform string `json:"terraform,omitempty"`
}

type SpecGuide struct {
	Layout    []Folder `json:"layout"`
	Questions []string `json:"questions"`
	Templates []File   `json:"templates"`
	Gate      []string `json:"gate"`
}

// ComputeOption is one way to run the service on the platform. Local says
// whether Nimbo's local emulator runs it ("runs locally") or the
// Terraform is for the real cloud only ("export only").
type ComputeOption struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Target         string   `json:"target"`
	TerraformTypes []string `json:"terraformTypes,omitempty"`
	When           string   `json:"when"`
	AvoidWhen      string   `json:"avoidWhen"`
	Local          string   `json:"local"`
}

type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Guide is the guidance for one Options combination. Sandbox tells how the
// code reaches the emulated services when it runs in Nimbo's local
// sandbox (verified per platform).
type Guide struct {
	Mode          string           `json:"mode"`
	Language      string           `json:"language,omitempty"`
	Style         string           `json:"style"`
	Platform      string           `json:"platform"`
	Target        string           `json:"target"`
	Principles    []string         `json:"principles"`
	Layout        []Folder         `json:"layout"`
	Files         []File           `json:"files,omitempty"`
	Packaging     Packaging        `json:"packaging"`
	Compute       []ComputeOption  `json:"compute"`
	Sandbox       []string         `json:"sandbox"`
	Testing       []string         `json:"testing"`
	Security      []string         `json:"security"`
	Observability []string         `json:"observability"`
	Spec          SpecGuide        `json:"spec"`
	Languages     []LanguageOption `json:"languages,omitempty"`
	Sources       []Source         `json:"sources"`
}

// Get returns the guidance for o, or an error naming the valid values when
// a field is unknown or the combination doesn't exist (with the target to
// use instead).
func Get(o Options) (Guide, error) {
	o, err := normalize(o)
	if err != nil {
		return Guide{}, err
	}
	g := Guide{
		Mode: o.Mode, Language: o.Language, Style: o.Style, Platform: o.Platform, Target: o.Target,
		Principles:    principles(o),
		Layout:        layout(o),
		Compute:       computeOptions(o.Platform),
		Sandbox:       sandboxAccess[o.Platform],
		Testing:       testPractices(o),
		Security:      security,
		Observability: observability(o.Platform),
		Spec:          specGuide,
		Sources:       sources,
	}
	if o.Mode == ModeFull && o.Language == "" {
		g.Languages = languageOptions(o)
		g.Packaging = Packaging{Kind: "pending", How: "Choose the language first (ask the user, or propose one of languages with its why), then call again with it to get the files and the packaging."}
		return g, nil
	}
	if g.Files, err = files(o); err != nil {
		return Guide{}, err
	}
	g.Packaging = packaging(o)
	return g, nil
}

func normalize(o Options) (Options, error) {
	clean := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	o = Options{Mode: clean(o.Mode), Language: clean(o.Language), Style: clean(o.Style), Platform: clean(o.Platform), Target: clean(o.Target)}
	if a, ok := languageAliases[o.Language]; ok {
		o.Language = a
	}
	if a, ok := platformAliases[o.Platform]; ok {
		o.Platform = a
	}
	if o.Mode == "" {
		o.Mode = ModeArchitecture
	}
	if o.Style == "" {
		o.Style = StyleService
	}
	if o.Platform == "" {
		o.Platform = "aws"
	}
	local := o.Platform == "docker" || o.Platform == "kubernetes"
	if o.Target == "" {
		o.Target = TargetFunction
		if local {
			o.Target = TargetContainer
		}
	}
	for _, f := range []struct {
		name, v string
		valid   []string
	}{
		{"mode", o.Mode, Modes}, {"style", o.Style, Styles}, {"platform", o.Platform, Platforms}, {"target", o.Target, Targets},
	} {
		if !slices.Contains(f.valid, f.v) {
			return o, fmt.Errorf("unknown %s %q: one of %s", f.name, f.v, strings.Join(f.valid, ", "))
		}
	}
	if o.Language != "" && !slices.Contains(Languages, o.Language) {
		return o, fmt.Errorf("unknown language %q: one of %s", o.Language, strings.Join(Languages, ", "))
	}
	if local && o.Target != TargetContainer {
		return o, fmt.Errorf("platform %s runs containers only: use target container", o.Platform)
	}
	if o.Mode == ModeArchitecture {
		// Smoke handlers prove the wiring, not the stack: JavaScript (or
		// Python when asked) runs without a build on every platform.
		if o.Language != "python" {
			o.Language = "javascript"
		}
	}
	if o.Mode == ModeFull && o.Target == TargetFunction && o.Platform != "aws" && o.Language != "" && !slices.Contains(nonAWSFunctionLanguages, o.Language) {
		return o, fmt.Errorf("%s functions on %s need the vendor's build tooling and are not emulated locally: use target container (%s) for %s, or javascript, typescript or python for functions",
			o.Language, o.Platform, containerService[o.Platform], o.Language)
	}
	return o, nil
}

// nonAWSFunctionLanguages have function skeletons on Azure, GCP and OCI.
var nonAWSFunctionLanguages = []string{"javascript", "typescript", "python"}

var languageAliases = map[string]string{
	"ts": "typescript", "node": "javascript", "nodejs": "javascript", "js": "javascript",
	"py": "python", "golang": "go", "c#": "csharp", "dotnet": "csharp", ".net": "csharp",
}

var platformAliases = map[string]string{
	"amazon": "aws", "azurerm": "azure", "google": "gcp", "gcloud": "gcp", "oracle": "oci",
	"compose": "docker", "docker-compose": "docker", "k8s": "kubernetes", "kube": "kubernetes",
}

// containerService names the managed container service per platform.
var containerService = map[string]string{
	"aws": "ECS Fargate", "azure": "Azure Container Apps", "gcp": "Cloud Run", "oci": "OCI Container Instances",
	"docker": "Docker Compose", "kubernetes": "Kubernetes",
}
