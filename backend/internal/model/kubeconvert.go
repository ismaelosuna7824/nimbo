package model

// Converting a docker workflow into a Kubernetes one
// (POST /workflows/{id}/convert/kubernetes).

// Convert formats (KubeConvertReport.Format): what the new workflow's
// manifests are written as.
const (
	ConvertManifests = KubeManifests // k8s/*.yaml
	ConvertKustomize = KubeKustomize // k8s/base + k8s/overlays/{dev,prod}
	ConvertHelm      = KubeHelm      // chart/ with values-dev/prod.yaml
)

// KubeConvertReport tells what a conversion produced and what it couldn't
// map.
type KubeConvertReport struct {
	Format    string `json:"format"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Sources and Builds are the new workflow's Kubernetes.Sources/Builds.
	Sources      []KubeSource       `json:"sources"`
	Builds       []KubeBuild        `json:"builds"`
	Environments []string           `json:"environments"`
	Objects      []KubeConvertObj   `json:"objects"`
	Mappings     []KubeConvertMap   `json:"mappings"`
	Unmapped     []KubeConvertIssue `json:"unmapped"`
}

// KubeConvertObj is one object written (for Helm: rendered with the
// default values).
type KubeConvertObj struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	File    string `json:"file"`
	Service string `json:"service,omitempty"` // the compose service it comes from
}

// KubeConvertMap is one compose feature and what it became.
type KubeConvertMap struct {
	Service string `json:"service,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// KubeConvertIssue is a compose feature that couldn't be mapped, or was
// mapped approximately: why, and what to do.
type KubeConvertIssue struct {
	Service  string `json:"service,omitempty"`
	Feature  string `json:"feature"`
	Detail   string `json:"detail"`
	Severity string `json:"severity"` // warning | info
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}
