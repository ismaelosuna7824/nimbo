package kuberules

import "github.com/ismaelosuna7824/nimbo/backend/internal/model"

// Well-Architected pillar shorthands for the table below.
const (
	sec  = model.PillarSecurity
	rel  = model.PillarReliability
	perf = model.PillarPerformance
	cost = model.PillarCost
	ops  = model.PillarOperations
)

// rulePillars maps every k8s_* rule to the Well-Architected pillars it
// serves, most relevant first (rule() reads it; TestRulePillars keeps it
// complete).
var rulePillars = map[string][]model.Pillar{
	// Errors: the renderer or the API server refuses them.
	"k8s_yaml":              {ops},
	"k8s_render":            {ops},
	"k8s_load":              {ops},
	"k8s_dependency":        {ops},
	"k8s_object":            {ops},
	"k8s_schema":            {ops},
	"k8s_removed_api":       {ops, rel},
	"k8s_duplicate":         {ops, rel},
	"k8s_workload_selector": {rel, ops},

	// Reliability.
	"k8s_deprecated_api":     {ops, rel},
	"k8s_no_requests":        {perf, rel, cost},
	"k8s_no_memory_limit":    {rel, perf, cost},
	"k8s_no_readiness_probe": {rel},
	"k8s_no_liveness_probe":  {rel},
	"k8s_probe_port":         {rel},
	"k8s_latest_tag":         {rel, ops},
	"k8s_local_image":        {rel, ops},
	"k8s_single_replica":     {rel},
	"k8s_hpa_no_requests":    {perf, rel},
	"k8s_no_hpa":             {perf, cost, rel},
	"k8s_ephemeral_data":     {rel},
	"k8s_pvc_storage_class":  {rel, perf, cost},
	"k8s_topology_spread":    {rel},
	"k8s_pdb":                {rel, ops},
	"k8s_rollout_strategy":   {rel, ops},
	"k8s_startup_probe":      {rel, perf},

	// Hygiene.
	"k8s_recommended_labels": {ops, cost},
	"k8s_resource_quota":     {cost, rel, perf},
	"k8s_image_digest":       {sec, rel},

	// Networking and wiring.
	"k8s_selector_mismatch": {rel},
	"k8s_service_port":      {rel},
	"k8s_ingress_backend":   {rel},
	"k8s_unknown_ref":       {rel, ops},
	"k8s_namespace_missing": {ops},
	"k8s_default_namespace": {ops, sec},
	"k8s_network_policy":    {sec},
	"k8s_ingress_tls":       {sec},

	// Security.
	"k8s_secret_env":           {sec},
	"k8s_secret_in_configmap":  {sec},
	"k8s_privileged":           {sec},
	"k8s_host_namespaces":      {sec},
	"k8s_host_path":            {sec, rel},
	"k8s_run_as_root":          {sec},
	"k8s_hardening":            {sec},
	"k8s_pss_namespace_label":  {sec},
	"k8s_host_port":            {sec, rel},
	"k8s_windows_host_process": {sec},
	"k8s_net_raw":              {sec},
	"k8s_capabilities":         {sec},
	"k8s_seccomp":              {sec},
	"k8s_secret_env_ref":       {sec},
	"k8s_automount_token":      {sec},
	"k8s_rbac_cluster_admin":   {sec},
	"k8s_rbac_system_masters":  {sec},
	"k8s_rbac_default_sa":      {sec},
	"k8s_rbac_wildcard":        {sec},
	"k8s_rbac_secrets":         {sec},
	"k8s_rbac_create_pods":     {sec},
	"k8s_rbac_escalate":        {sec},
	"k8s_rbac_create_pv":       {sec},
	"k8s_rbac_node_proxy":      {sec},
	"k8s_rbac_csr_approval":    {sec},
	"k8s_rbac_webhook_config":  {sec},
	"k8s_rbac_sa_token":        {sec},
}
