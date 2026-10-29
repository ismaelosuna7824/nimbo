package compose

import "github.com/ismaelosuna7824/nimbo/backend/internal/model"

// Well-Architected pillar shorthands for the table below.
const (
	sec  = model.PillarSecurity
	rel  = model.PillarReliability
	perf = model.PillarPerformance
	cost = model.PillarCost
	ops  = model.PillarOperations
)

// rulePillars maps every dk_* rule to the Well-Architected pillars it
// serves, most relevant first (rule() reads it; TestRulePillars keeps it
// complete).
var rulePillars = map[string][]model.Pillar{
	// Errors: docker compose refuses the project.
	"dk_yaml":               {ops},
	"dk_schema":             {ops},
	"dk_load":               {ops},
	"dk_unknown_ref":        {rel, ops},
	"dk_port_clash":         {rel, ops},
	"dk_missing_context":    {ops},
	"dk_missing_dockerfile": {ops},
	"dk_dockerfile_syntax":  {ops},
	"dk_outside_project":    {ops},
	"dk_env_syntax":         {ops},

	// Services.
	"dk_environment_file":    {ops},
	"dk_unresolved_var":      {rel, ops},
	"dk_missing_env_file":    {rel, ops},
	"dk_no_healthcheck":      {rel},
	"dk_depends_healthy":     {rel},
	"dk_depends_completed":   {rel},
	"dk_missing_healthcheck": {rel, ops},
	"dk_healthcheck_timing":  {perf, rel},
	"dk_no_restart":          {rel},
	"dk_no_limits":           {perf, rel, cost},
	"dk_log_rotation":        {ops, rel},
	"dk_anon_volume":         {rel},
	"dk_bind_deps":           {perf},
	"dk_no_init":             {rel},
	"dk_expose_mismatch":     {rel, ops},
	"dk_pids_limit":          {rel, sec},

	// Images and builds.
	"dk_latest_tag":   {rel, ops},
	"dk_single_stage": {perf, cost, sec},
	"dk_heavy_base":   {perf, cost, sec},
	"dk_dockerignore": {perf, sec},
	"dk_layer_order":  {perf},
	"dk_cache_mount":  {perf},
	"dk_apt_install":  {perf, cost},
	"dk_pip_cache":    {perf, cost},
	"dk_apk_cache":    {perf, cost},
	"dk_add":          {sec, ops},
	"dk_shell_form":   {rel},
	"dk_workdir":      {ops},
	"dk_update_alone": {sec, rel},
	"dk_add_any":      {sec},

	// Security.
	"dk_secret_env":          {sec},
	"dk_secret_build":        {sec},
	"dk_root_user":           {sec},
	"dk_db_published":        {sec},
	"dk_docker_sock":         {sec},
	"dk_privileged":          {sec},
	"dk_hardening":           {sec},
	"dk_secret_file":         {sec},
	"dk_network_internal":    {sec},
	"dk_cap_add":             {sec},
	"dk_net_raw":             {sec},
	"dk_read_only":           {sec},
	"dk_no_new_privileges":   {sec},
	"dk_apparmor_unconfined": {sec},
	"dk_selinux_disabled":    {sec},
	"dk_seccomp_unconfined":  {sec},
	"dk_host_dirs":           {sec},
	"dk_host_ipc":            {sec},
	"dk_host_uts":            {sec},
	"dk_host_userns":         {sec},
	"dk_devices":             {sec},
	"dk_mount_propagation":   {sec},
	"dk_default_bridge":      {sec},
	"dk_port_interface":      {sec},
	"dk_privileged_port":     {sec},
}
