// Package patterns is Nimbo's catalog of architecture styles and cloud design
// patterns: what problem each solves, when to use it and when it is
// over-engineering, its trade-offs, the practices a senior engineer insists
// on, and how it maps to each provider's services (exact Terraform types), to
// local Docker Compose (curated image IDs) and to Kubernetes (kinds).
//
// The catalog is based on the Azure Architecture Center (cloud design
// patterns and architecture styles), AWS Prescriptive Guidance cloud design
// patterns, the Google Cloud Architecture Center and microservices.io.
package patterns

import (
	"slices"
	"strings"
)

// Source is an authoritative reference for a pattern.
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Block is how a pattern is built on one platform.
type Block struct {
	Platform string   `json:"platform"` // aws | azure | gcp | oci | docker | kubernetes
	Types    []string `json:"types"`    // exact Terraform resource types, compose image IDs, or Kubernetes kinds
	How      string   `json:"how"`      // how the pieces fit on this platform
	Local    string   `json:"local"`    // "runs locally" | "export only: …" | "local equivalent: …"
}

// Pattern is an architecture style or a cloud design pattern.
type Pattern struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"` // "style" | "pattern"
	Problem   string   `json:"problem"`
	UseWhen   []string `json:"useWhen"`
	AvoidWhen []string `json:"avoidWhen"`
	Tradeoffs []string `json:"tradeoffs"`
	Practices []string `json:"practices"`
	Blocks    []Block  `json:"blocks"`
	Related   []string `json:"related"`
	Templates []string `json:"templates,omitempty"`
	Keywords  []string `json:"-"`
	Sources   []Source `json:"sources"`
}

// Kinds.
const (
	KindStyle   = "style"
	KindPattern = "pattern"
)

// Platforms.
const (
	AWS        = "aws"
	Azure      = "azure"
	GCP        = "gcp"
	OCI        = "oci"
	Docker     = "docker"
	Kubernetes = "kubernetes"
)

// Local values (same wording as the advisor's).
const (
	LocalRuns       = "runs locally"
	LocalExportOnly = "export only: not emulated locally"
)

// notEmulated lists the Terraform types used by the catalog that the local
// Floci emulators do not deploy (kept in sync with the catalog's
// FlociSupported flag by a test).
var notEmulated = map[string]bool{
	"azurerm_application_gateway":                    true,
	"azurerm_cdn_frontdoor_firewall_policy":          true,
	"azurerm_cdn_frontdoor_profile":                  true,
	"azurerm_linux_function_app":                     true,
	"azurerm_stream_analytics_job":                   true,
	"azurerm_web_application_firewall_policy":        true,
	"azurerm_application_insights_standard_web_test": true,
	"azurerm_static_web_app":                         true,
	"azurerm_logic_app_workflow":                     true,
	"azurerm_traffic_manager_profile":                true,
	"google_compute_backend_bucket":                  true,
	"google_compute_backend_service":                 true,
	"google_compute_global_forwarding_rule":          true,
	"google_compute_security_policy":                 true,
	"google_compute_url_map":                         true,
	"google_cloud_tasks_queue":                       true,
	"google_dataflow_job":                            true,
	"oci_functions_application":                      true, // floci-oci serves Functions 20181201; oracle/oci >= 9.3 calls 20260325
	"oci_functions_function":                         true,
	"google_eventarc_trigger":                        true,
	"google_firestore_database":                      true,
	"google_monitoring_alert_policy":                 true,
	"google_monitoring_uptime_check_config":          true,
	"google_redis_instance":                          true,
	"google_spanner_instance":                        true,
	"google_workflows_workflow":                      true,
	"google_identity_platform_config":                true,
	"google_parameter_manager_parameter":             true,
	"google_bigtable_instance":                       true,
	"oci_apigateway_deployment":                      true,
	"oci_apigateway_gateway":                         true,
	"oci_core_instance_pool":                         true,
	"oci_database_autonomous_database":               true,
	"oci_events_rule":                                true,
	"oci_health_checks_http_monitor":                 true,
	"oci_dns_record":                                 true,
	"oci_identity_domain":                            true,
	"oci_load_balancer_load_balancer":                true,
	"oci_monitoring_alarm":                           true,
	"oci_nosql_table":                                true,
	"oci_ons_notification_topic":                     true,
	"oci_ons_subscription":                           true,
	"oci_redis_redis_cluster":                        true,
	"oci_sch_service_connector":                      true,
	"oci_resource_scheduler_schedule":                true,
	"oci_waf_web_app_firewall":                       true,
	"oci_waf_web_app_firewall_policy":                true,
}

// tf builds a Terraform block; Local names the types the emulator lacks.
func tf(platform, how string, types ...string) Block {
	var missing []string
	for _, t := range types {
		if notEmulated[t] {
			missing = append(missing, t)
		}
	}
	local := LocalRuns
	if len(missing) > 0 {
		local = LocalExportOnly + " (" + strings.Join(missing, ", ") + ")"
	}
	return Block{Platform: platform, Types: types, How: how, Local: local}
}

func aws(how string, types ...string) Block   { return tf(AWS, how, types...) }
func azure(how string, types ...string) Block { return tf(Azure, how, types...) }
func gcp(how string, types ...string) Block   { return tf(GCP, how, types...) }
func oci(how string, types ...string) Block   { return tf(OCI, how, types...) }

// equivalent marks a local block as a stand-in for the cloud managed service.
func (b Block) equivalent(what string) Block {
	b.Local = "local equivalent: " + what
	return b
}

// docker builds a Compose block from curated image IDs.
func docker(how string, images ...string) Block {
	return Block{Platform: Docker, Types: images, How: how, Local: LocalRuns}
}

// kube builds a Kubernetes block from object kinds.
func kube(how string, kinds ...string) Block {
	return Block{Platform: Kubernetes, Types: kinds, How: how, Local: LocalRuns}
}

// Source URL bases.
const (
	azPatterns = "https://learn.microsoft.com/en-us/azure/architecture/patterns/"
	azStyles   = "https://learn.microsoft.com/en-us/azure/architecture/guide/architecture-styles/"
	awsPG      = "https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/"
	msio       = "https://microservices.io/patterns/"
)

func azP(title, page string) Source {
	return Source{"Azure Architecture Center: " + title, azPatterns + page}
}
func azS(title, page string) Source {
	return Source{"Azure Architecture Center: " + title, azStyles + page}
}
func awsP(title, page string) Source {
	return Source{"AWS Prescriptive Guidance: " + title, awsPG + page}
}
func msioP(title, page string) Source { return Source{"microservices.io: " + title, msio + page} }

var (
	all  []Pattern
	byID map[string]int
)

func init() {
	all = slices.Concat(styles(), messaging(), data(), resilience(), edge(), migration(), operations())
	byID = make(map[string]int, len(all))
	for i, p := range all {
		byID[p.ID] = i
	}
}

// All returns every pattern: styles first, then patterns, each by ID.
func All() []Pattern {
	out := slices.Clone(all)
	slices.SortStableFunc(out, func(a, b Pattern) int {
		if a.Kind != b.Kind {
			if a.Kind == KindStyle {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Get returns the pattern id.
func Get(id string) (Pattern, bool) {
	i, ok := byID[id]
	if !ok {
		return Pattern{}, false
	}
	return all[i], true
}

// block returns p's block for platform.
func (p Pattern) block(platform string) (Block, bool) {
	for _, b := range p.Blocks {
		if b.Platform == platform {
			return b, true
		}
	}
	return Block{}, false
}
