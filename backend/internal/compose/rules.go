package compose

import (
	"bytes"
	"slices"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Rule is one docker check (codes dk_*); Why explains it in diagnostics.
type Rule struct {
	model.BestPracticeRule
	Why string
}

// Rule categories: errors are "docker" (docker compose refuses the project
// and they can't be turned off); advice is grouped for Settings.
const (
	catErrors   = "docker"
	catBuild    = "docker-build"    // Dockerfiles: image size, build speed
	catCompose  = "docker-compose"  // services: reliability, runtime performance
	catSecurity = "docker-security" // credentials, privileges, exposure
)

const (
	docsBestPractices = "https://docs.docker.com/build/building/best-practices/"
	docsComposeSvc    = "https://docs.docker.com/reference/compose-file/services/"
)

func rule(code, title, category string, sev model.Severity, description, docs, why string) Rule {
	return Rule{model.BestPracticeRule{Code: code, Title: title, Category: category, Severity: sev, Description: description, Docs: docs, Pillars: rulePillars[code]}, why}
}

func optIn(r Rule) Rule {
	r.DefaultOff = true
	return r
}

// rules lists every docker check. Errors are the problems docker compose
// itself refuses; warnings and info are advice the user can turn off in
// Settings (best-practice rules). DefaultOff rules are opt-in.
var rules = []Rule{
	rule("dk_yaml", "YAML syntax", catErrors, model.SeverityError,
		"A compose file must be valid YAML.", "https://docs.docker.com/reference/compose-file/",
		"docker compose can't read a file that is not valid YAML; nothing in it is applied."),
	rule("dk_schema", "Compose specification", catErrors, model.SeverityError,
		"Every attribute must follow the Compose specification.", "https://docs.docker.com/reference/compose-file/",
		"docker compose validates the files against the Compose specification and refuses unknown attributes or values of the wrong type."),
	rule("dk_load", "Project can be loaded", catErrors, model.SeverityError,
		"The files must merge into a valid project (overrides, include, extends).", "https://docs.docker.com/compose/how-tos/multiple-compose-files/",
		"docker compose merges every file, include and extends into one project before running it; this step failed."),
	rule("dk_unknown_ref", "Unknown references", catErrors, model.SeverityError,
		"depends_on, links, networks, volumes, configs and secrets must name something the project declares.", docsComposeSvc,
		"docker compose refuses a project whose services reference a service, network, volume, config or secret that does not exist."),
	rule("dk_port_clash", "Host port clashes", catErrors, model.SeverityError,
		"Two services, or several replicas of one service, can't publish the same host port.", docsComposeSvc+"#ports",
		"Only one container can bind a host port: the second service (or replica) fails to start with \"port is already allocated\"."),
	rule("dk_missing_context", "Build context exists", catErrors, model.SeverityError,
		"A build context must be a folder of the workflow.", "https://docs.docker.com/build/concepts/context/",
		"docker compose build sends the context folder to the builder; a missing folder fails the build."),
	rule("dk_missing_dockerfile", "Dockerfile exists", catErrors, model.SeverityError,
		"The Dockerfile a service builds must exist.", "https://docs.docker.com/reference/compose-file/build/#dockerfile",
		"The build fails when the Dockerfile named by build.dockerfile (default: Dockerfile in the context) is missing."),
	rule("dk_dockerfile_syntax", "Dockerfile syntax", catErrors, model.SeverityError,
		"Dockerfiles must parse (instructions, FROM first, heredocs closed).", "https://docs.docker.com/reference/dockerfile/",
		"BuildKit refuses a Dockerfile with unknown instructions, instructions before FROM or an unterminated heredoc."),
	rule("dk_outside_project", "Files inside the workflow", catErrors, model.SeverityError,
		"include and extends files must be inside the workflow files.", "https://docs.docker.com/compose/how-tos/multiple-compose-files/include/",
		"Nimbo runs the project from the workflow's own files; a file outside them is not part of the workflow."),
	rule("dk_env_syntax", ".env syntax", catErrors, model.SeverityError,
		".env must hold KEY=value lines.", "https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/#env-file",
		"docker compose reads .env to interpolate the compose files; a line it can't parse stops it."),

	// Environments.
	rule("dk_environment_file", "Environment files", catCompose, model.SeverityInfo,
		"Env and override files named like an environment must give a usable environment.", "https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/#env-file",
		"Nimbo turns compose.<env>.yaml, .env.<env> and <env>.env into environments; files it can't use that way (an invalid name, two env files for one environment, local-only .env.local files) are ignored, so their values never reach a run."),

	// Docker Compose services.
	rule("dk_unresolved_var", "Variables without a value", catCompose, model.SeverityWarning,
		"${VAR} used in a compose file should have a value in .env or a default.", "https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/",
		"An unset variable is replaced by an empty string (docker compose only warns), which usually breaks an image tag, a port or a connection string."),
	rule("dk_missing_env_file", "env_file exists", catCompose, model.SeverityWarning,
		"Files listed in env_file should exist.", docsComposeSvc+"#env_file",
		"docker compose fails when a required env_file is missing (and silently skips an optional one)."),
	rule("dk_no_healthcheck", "service_healthy needs a healthcheck", catCompose, model.SeverityWarning,
		"A service other services wait for with condition: service_healthy needs a healthcheck.", "https://docs.docker.com/compose/how-tos/startup-order/",
		"Without a healthcheck the container never becomes healthy, so the services waiting for it never start (docker compose up fails with \"dependency failed to start\")."),
	rule("dk_depends_healthy", "Wait for databases to be healthy", catCompose, model.SeverityWarning,
		"depends_on a database, cache or broker that has a healthcheck should use condition: service_healthy.", "https://docs.docker.com/compose/how-tos/startup-order/",
		"service_started only waits for the container to exist: the app starts while the database is still initializing, fails its first connections and crash-loops or serves errors until it is ready."),
	rule("dk_depends_completed", "Don't wait for a server to complete", catCompose, model.SeverityError,
		"condition: service_completed_successfully is for one-off jobs (migrations, seeds); a service with a restart policy, healthcheck, published ports or a database image never completes.", "https://docs.docker.com/compose/how-tos/startup-order/",
		"The dependent waits for an exit that never comes: it stays Created and docker compose up hangs until it times out."),
	rule("dk_missing_healthcheck", "Healthchecks on services", catCompose, model.SeverityInfo,
		"Servers built here, and images other services depend on, should declare a healthcheck (compose healthcheck: or Dockerfile HEALTHCHECK).", "https://docs.docker.com/reference/dockerfile/#healthcheck",
		"Without a healthcheck Docker only knows the process is running, not that it answers: depends_on can't wait for readiness, restarts don't catch a hung server and Nimbo can't show the service as healthy."),
	rule("dk_healthcheck_timing", "Healthcheck timing", catCompose, model.SeverityInfo,
		"Healthchecks should run every 5s or more, with a timeout shorter than the interval.", docsComposeSvc+"#healthcheck",
		"Each check starts a new process in the container: an interval under 5s burns CPU on every container all day (and wakes laptops' Docker VM), and a timeout longer than the interval lets slow checks pile up."),
	rule("dk_no_restart", "Restart policies", catCompose, model.SeverityInfo,
		"Long-running services should set restart: (unless-stopped or on-failure).", docsComposeSvc+"#restart",
		"The default is no: a service that crashes, or the whole project after Docker restarts, stays down until someone runs up again."),
	optIn(rule("dk_no_limits", "Resource limits", catCompose, model.SeverityInfo,
		"Services should cap their memory (and CPU) with deploy.resources.limits.", "https://docs.docker.com/reference/compose-file/deploy/#resources",
		"Without limits one container (a JVM, a search engine, a leaking app) can take all of Docker's memory: the other services get OOM-killed and the machine swaps.")),
	rule("dk_log_rotation", "Log rotation", catCompose, model.SeverityInfo,
		"A json-file logging configuration should set max-size (and max-file).", "https://docs.docker.com/engine/logging/drivers/json-file/",
		"json-file keeps every log line forever by default: a chatty service fills the disk (Docker Desktop's VM disk on macOS/Windows) and slows docker logs."),
	rule("dk_anon_volume", "Named volumes for data", catCompose, model.SeverityWarning,
		"Databases should keep their data in a named volume (or a bind mount), not an anonymous one.", "https://docs.docker.com/engine/storage/volumes/",
		"An anonymous volume has a random name: docker compose down orphans it (down -v deletes it) and the next up starts with an empty database."),
	rule("dk_bind_deps", "Dependency folders in bind mounts", catCompose, model.SeverityWarning,
		"On macOS and Windows, node_modules, vendor and virtualenvs should live in a named volume, not a bind mount.", "https://docs.docker.com/engine/storage/volumes/",
		"Bind mounts cross the Docker Desktop VM boundary: tens of thousands of small dependency files make installs, module resolution and file watching several times slower than in a volume."),
	rule("dk_no_init", "An init process for Node.js and Python", catCompose, model.SeverityInfo,
		"node and python running as PID 1 should run under an init (init: true or tini).", docsComposeSvc+"#init",
		"PID 1 gets no default signal handlers: node and python ignore SIGTERM, so docker compose stop waits 10s and kills them (no graceful shutdown), and orphaned child processes are never reaped."),
	rule("dk_expose_mismatch", "Published ports match the image", catCompose, model.SeverityInfo,
		"Container ports published should be ones the image listens on (EXPOSE, or the database's port).", "https://docs.docker.com/reference/dockerfile/#expose",
		"Publishing a container port nothing listens on gives \"connection reset\" on the host port while the service looks healthy."),

	// Images and builds.
	rule("dk_latest_tag", "Pinned image tags", catBuild, model.SeverityInfo,
		"Images and Dockerfile FROM lines should name a version tag instead of latest (or no tag).", docsBestPractices+"#pin-base-image-versions",
		"latest changes whenever the publisher pushes: the same files can build or start a different, possibly incompatible, version tomorrow, and every pull re-downloads it."),
	rule("dk_single_stage", "Multi-stage builds for compiled code", catBuild, model.SeverityWarning,
		"Go, Rust, Java, .NET and bundled JavaScript should be built in a build stage and copied into a small runtime stage.", "https://docs.docker.com/build/building/multi-stage/",
		"A single-stage build ships the compiler, SDK, build cache and sources with the app: images of 1 GB or more instead of tens of MB, slower pulls and starts, and a larger attack surface."),
	rule("dk_heavy_base", "Slim runtime base images", catBuild, model.SeverityInfo,
		"The final stage should run on a slim, alpine, distroless or JRE image rather than a full language or SDK image.", docsBestPractices+"#choose-the-right-base-image",
		"Full language images carry compilers, headers and hundreds of OS packages the app never uses: several hundred MB more to pull, store and scan."),
	rule("dk_dockerignore", ".dockerignore for heavy build contexts", catBuild, model.SeverityWarning,
		"A build context with node_modules, .git, virtualenvs or build output should have a .dockerignore excluding them.", "https://docs.docker.com/build/concepts/context/#dockerignore-files",
		"Every build uploads the whole context to the builder before the first step runs, and COPY . copies host-built dependencies into the image (wrong platform, cache busted on every change)."),
	rule("dk_layer_order", "Cache-friendly layer order", catBuild, model.SeverityWarning,
		"Copy the dependency manifests (package.json, go.mod, requirements.txt, pom.xml, *.csproj…) and install dependencies before copying the sources.", "https://docs.docker.com/build/cache/optimize/#order-your-layers",
		"A layer is rebuilt when anything copied before it changes: with COPY . first, every source edit re-downloads and reinstalls every dependency."),
	optIn(rule("dk_cache_mount", "BuildKit cache mounts for package managers", catBuild, model.SeverityInfo,
		"Dependency installs can keep the package manager's download cache in a cache mount (RUN --mount=type=cache).", "https://docs.docker.com/build/cache/optimize/#use-cache-mounts",
		"When the dependency layer is rebuilt (a manifest changed), a cache mount reuses the packages already downloaded instead of fetching all of them again; the cache never ends up in the image.")),
	rule("dk_apt_install", "apt-get without extras or leftovers", catBuild, model.SeverityInfo,
		"apt-get install should use --no-install-recommends and remove /var/lib/apt/lists/* in the same RUN.", docsBestPractices+"#apt-get",
		"Recommended packages and the package lists add tens of MB to the layer; removing them in a later RUN does not shrink the image (the earlier layer keeps them)."),
	rule("dk_pip_cache", "pip without a cache in the image", catBuild, model.SeverityInfo,
		"pip install should use --no-cache-dir (or a cache mount).", "https://pip.pypa.io/en/stable/topics/caching/",
		"pip keeps every downloaded wheel in /root/.cache/pip inside the layer: often as large as the installed packages themselves."),
	rule("dk_apk_cache", "apk without a cache in the image", catBuild, model.SeverityInfo,
		"apk add should use --no-cache.", "https://github.com/alpinelinux/docker-alpine/blob/master/README.md",
		"Without --no-cache, apk keeps the package index in /var/cache/apk inside the layer."),
	rule("dk_add", "COPY instead of ADD", catBuild, model.SeverityInfo,
		"Use COPY for local files; download URLs with RUN curl (or ADD --checksum).", docsBestPractices+"#add-or-copy",
		"ADD silently extracts local archives and downloads URLs without verifying them; COPY says exactly what happens, and an unpinned download makes builds unreproducible."),
	rule("dk_shell_form", "Exec-form CMD and ENTRYPOINT", catBuild, model.SeverityWarning,
		"CMD and ENTRYPOINT should use the exec (JSON) form: CMD [\"node\", \"server.js\"].", "https://docs.docker.com/reference/dockerfile/#shell-and-exec-form",
		"The shell form runs the app as a child of /bin/sh -c: sh is PID 1 and doesn't forward SIGTERM, so docker stop waits 10s and kills the app without a graceful shutdown."),
	rule("dk_workdir", "WORKDIR instead of cd", catBuild, model.SeverityInfo,
		"Use WORKDIR to change directory instead of RUN cd … &&.", docsBestPractices+"#workdir",
		"cd only lasts for that RUN: later instructions run elsewhere, which is easy to miss, and WORKDIR documents (and creates) the directory."),

	// Security.
	rule("dk_secret_env", "Secrets written in environment", catSecurity, model.SeverityWarning,
		"Passwords, tokens and keys should not be written literally in compose files.", "https://docs.docker.com/compose/how-tos/use-secrets/",
		"Compose files are usually committed: a literal secret ends up in the repository history. Values from .env (kept out of git) or secrets: are not."),
	rule("dk_secret_build", "Secrets in build arguments", catSecurity, model.SeverityWarning,
		"Tokens and passwords should reach a build through BuildKit secrets, not ARG or ENV.", "https://docs.docker.com/build/building/secrets/",
		"ARG values are recorded in the image history and ENV values in the image config: anyone who can pull the image can read them (docker history, docker inspect)."),
	rule("dk_root_user", "Containers run as a non-root user", catSecurity, model.SeverityInfo,
		"Images built from a Dockerfile should switch to a non-root USER.", docsBestPractices+"#user",
		"A process running as root inside the container has root privileges over everything mounted into it and is one kernel bug away from root on the host."),
	rule("dk_db_published", "Databases not published on every interface", catSecurity, model.SeverityWarning,
		"Databases, caches and brokers should publish on 127.0.0.1 (or not at all: other services reach them by name).", docsComposeSvc+"#ports",
		"\"5432:5432\" binds every network interface: anyone on the same Wi-Fi or LAN reaches the database with its development password, and on Linux Docker's rules bypass the host firewall."),
	rule("dk_docker_sock", "Docker socket mounts", catSecurity, model.SeverityWarning,
		"Containers should not mount the Docker socket unless they manage Docker on purpose.", "https://docs.docker.com/engine/security/#docker-daemon-attack-surface",
		"Access to /var/run/docker.sock is root on the host (even mounted read-only): the container can start a privileged container that mounts /."),
	rule("dk_privileged", "Host-level privileges", catSecurity, model.SeverityWarning,
		"Avoid privileged: true, network_mode: host and pid: host.", docsComposeSvc+"#privileged",
		"These switch off the container's isolation: privileged gives every device and capability, host networking skips the project network and its port mapping, host PID shows (and lets it signal) every host process."),
	optIn(rule("dk_hardening", "Hardened containers", catSecurity, model.SeverityInfo,
		"Services should run with read_only: true, cap_drop: [ALL] and security_opt: [no-new-privileges:true].", "https://cheatsheetseries.owasp.org/cheatsheets/Docker_Security_Cheat_Sheet.html",
		"A read-only root filesystem, no Linux capabilities and no privilege escalation (setuid) limit what an attacker can do after compromising the app.")),
}

var ruleByCode = func() map[string]Rule {
	m := map[string]Rule{}
	for _, r := range rules {
		m[r.Code] = r
	}
	return m
}()

// Rules lists the docker checks (GET /best-practices/rules).
func Rules() []model.BestPracticeRule {
	out := make([]model.BestPracticeRule, len(rules))
	for i, r := range rules {
		out[i] = r.BestPracticeRule
	}
	return out
}

// RuleWhy is why a docker rule matters ("" for unknown codes).
func RuleWhy(code string) string { return ruleByCode[code].Why }

// FilterSettings drops warning/info findings of rules turned off in
// Settings, and of opt-in rules not turned on. Errors always stay.
func FilterSettings(diags []model.Diagnostic, cfg model.BestPracticeSettings) []model.Diagnostic {
	return slices.DeleteFunc(diags, func(d model.Diagnostic) bool {
		if d.Severity == model.SeverityError {
			return false
		}
		if cfg.Disabled || slices.Contains(cfg.DisabledRules, d.Code) {
			return true
		}
		return ruleByCode[d.Code].DefaultOff && !slices.Contains(cfg.EnabledRules, d.Code)
	})
}

func parseDotenv(data []byte, env map[string]string) (map[string]string, error) {
	return dotenv.ParseWithLookup(bytes.NewReader(data), func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
}
