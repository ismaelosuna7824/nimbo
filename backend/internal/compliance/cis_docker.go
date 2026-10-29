package compliance

// CIS Docker Benchmark v1.8.0 (July 2025). Recommendation numbers and titles follow that release; cisecurity.org
// also lists a v1.9.0 (September 2026) whose recommendation list isn't public yet, so this pack moves to it once it
// can be verified. A compose design shows the container runtime settings (section 5) and the Dockerfiles it builds
// (section 4); the host, the daemon and its files (sections 1-3), operations (6) and Swarm (7) live outside the
// design and are manual, grouped by the benchmark's own subsections.

const (
	dockerHost    = "Host Configuration"
	dockerDaemon  = "Docker Daemon Configuration"
	dockerFiles   = "Docker Daemon Configuration Files"
	dockerImages  = "Container Images and Build File Configuration"
	dockerRuntime = "Container Runtime Configuration"
	dockerOps     = "Docker Security Operations"
	dockerSwarm   = "Docker Swarm Configuration"
	dockerOnHost  = "Check it on every host that runs the containers (docker-bench-security or CIS-CAT automate most of it), then attest it. Docker Desktop's VM is managed by Docker: attest it for the servers. "
	dockerPulled  = " Images pulled as is aren't inspected: check what the publisher documents."
)

var dockerService = []string{"service"}

func init() {
	Register(Framework{
		ID:       "cis-docker-1.8",
		Name:     "CIS Docker Benchmark",
		Version:  "1.8.0",
		Platform: "docker",
		Source:   "https://www.cisecurity.org/benchmark/docker",
		Controls: []Control{
			// 1 Host Configuration
			{ID: "1.1", Title: "Linux hosts specific configuration (1.1.1-1.1.18)", Section: dockerHost, Manual: true,
				Guidance: dockerOnHost + "Keep /var/lib/docker on its own partition, limit the docker group to trusted administrators, and add auditd rules for dockerd, containerd, runc and the Docker files and sockets the benchmark lists."},
			{ID: "1.2", Title: "General configuration (1.2.1-1.2.2)", Section: dockerHost, Manual: true,
				Guidance: dockerOnHost + "Harden the host OS (its own CIS benchmark) and keep Docker Engine on a supported, patched release."},
			// 2-3 Docker daemon
			{ID: "2", Title: "Docker daemon configuration (2.1-2.19)", Section: dockerDaemon, Manual: true,
				Guidance: dockerOnHost + "Review /etc/docker/daemon.json: icc false on the default bridge, log-level info, iptables on, no insecure-registries, overlay2 storage, TLS on any TCP socket, userns-remap, authorization plugins, centralized logging, no-new-privileges, live-restore, userland-proxy false, no experimental features."},
			{ID: "3", Title: "Docker daemon configuration files (3.1-3.24)", Section: dockerFiles, Manual: true,
				Guidance: dockerOnHost + "docker.service, docker.socket, /etc/docker, daemon.json, the TLS certificates and keys, and the Docker and containerd sockets must be owned by root (the Docker socket by root:docker) with the restrictive modes the benchmark gives."},

			// 4 Container Images and Build File Configuration
			{ID: "4.1", Title: "Ensure that a user for the container has been created", Section: dockerImages,
				Rules: []string{"dk_root_user"}, AppliesTo: dockerService,
				Guidance: "Nimbo checks the services built from a Dockerfile: the final stage must switch to a non-root USER (or the service sets user:)." + dockerPulled},
			{ID: "4.2", Title: "Ensure that containers use only trusted base images", Section: dockerImages, Manual: true, AppliesTo: dockerService,
				Guidance: "Use official or verified publisher images (or your own registry's), pinned to a version or digest; attest once every image and FROM line is from a source you trust."},
			{ID: "4.3", Title: "Ensure that unnecessary packages are not installed in the container", Section: dockerImages, Manual: true, AppliesTo: dockerService,
				Guidance: "Build on slim, alpine or distroless bases and install with --no-install-recommends (Nimbo's Slim runtime base images and apt-get rules help); attest after reviewing what each image installs."},
			{ID: "4.4", Title: "Ensure images are scanned and rebuilt to include security patches", Section: dockerImages, Manual: true, AppliesTo: dockerService,
				Guidance: "Scan every image (docker scout cves, Trivy, Grype) in CI and rebuild on new base image releases; attest once the scan runs on every build."},
			{ID: "4.5", Title: "Ensure Content trust for Docker is Enabled", Section: dockerImages, Manual: true, AppliesTo: dockerService,
				Guidance: "Set DOCKER_CONTENT_TRUST=1 where images are pulled and built (or verify signatures with cosign/Notation in CI), then attest."},
			{ID: "4.6", Title: "Ensure that HEALTHCHECK instructions have been added to container images", Section: dockerImages,
				Rules: []string{"dk_missing_healthcheck"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags servers built here with neither a Dockerfile HEALTHCHECK nor a compose healthcheck, and images other services depend on that have a known healthcheck but don't declare it." + dockerPulled},
			{ID: "4.7", Title: "Ensure update instructions are not used alone in Dockerfiles", Section: dockerImages,
				Rules: []string{"dk_update_alone"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags RUN instructions of the Dockerfiles built here that update or upgrade packages (apt-get, apk, yum, dnf, microdnf, zypper) without installing in the same RUN."},
			{ID: "4.8", Title: "Ensure setuid and setgid permissions are removed", Section: dockerImages, Manual: true, AppliesTo: dockerService,
				Guidance: "List them with find / -perm /6000 -type f in each image and remove the ones the app doesn't need (RUN find / -perm /6000 -type f -exec chmod a-s {} + in the final stage); security_opt no-new-privileges (5.26) also neutralizes them at runtime."},
			{ID: "4.9", Title: "Ensure that COPY is used instead of ADD in Dockerfiles", Section: dockerImages,
				Rules: []string{"dk_add_any"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags every ADD in the stages a service builds, archives and checksummed downloads included."},
			{ID: "4.10", Title: "Ensure secrets are not stored in Dockerfiles", Section: dockerImages,
				Rules: []string{"dk_secret_build", "dk_secret_file"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags ARG and ENV instructions that carry passwords, tokens or keys, and COPY/ADD instructions that put credential files (.env, .npmrc, .pypirc, .netrc, SSH keys, ~/.aws) into the image, named or through COPY . without a .dockerignore entry, in the Dockerfiles built here."},
			{ID: "4.11", Title: "Ensure only verified packages are installed", Section: dockerImages, Manual: true, AppliesTo: dockerService,
				Guidance: "Install from the distribution's signed repositories (no --allow-unauthenticated, no curl | sh) and verify the checksums of anything downloaded; attest after reviewing the Dockerfiles."},
			{ID: "4.12", Title: "Ensure all signed artifacts are validated", Section: dockerImages, Manual: true, AppliesTo: dockerService,
				Guidance: "Verify signatures of the binaries and archives the builds fetch (gpg --verify, cosign verify-blob); attest once every download is verified."},

			// 5 Container Runtime Configuration
			{ID: "5.1", Title: "Ensure swarm mode is not Enabled, if not needed", Section: dockerRuntime, Manual: true,
				Guidance: dockerOnHost + "docker info shows Swarm: inactive unless the host is part of a swarm on purpose."},
			{ID: "5.2", Title: "Ensure that, if applicable, an AppArmor Profile is enabled", Section: dockerRuntime,
				Rules: []string{"dk_apparmor_unconfined"}, AppliesTo: dockerService,
				Guidance: "Docker confines containers with docker-default on hosts with AppArmor; Nimbo flags services that opt out with security_opt apparmor=unconfined. Whether the host runs AppArmor is a host check."},
			{ID: "5.3", Title: "Ensure that, if applicable, SELinux security options are set", Section: dockerRuntime,
				Rules: []string{"dk_selinux_disabled"}, AppliesTo: dockerService,
				Guidance: "On SELinux hosts with the daemon's selinux-enabled, containers get labels by default; Nimbo flags services that switch them off with security_opt label=disable. Custom levels or types are a host-specific choice."},
			{ID: "5.4", Title: "Ensure that Linux kernel capabilities are restricted within containers", Section: dockerRuntime,
				Rules: []string{"dk_cap_add", "dk_net_raw"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags services adding ALL, SYS_ADMIN, NET_ADMIN, SYS_PTRACE or SYS_MODULE, and services that keep NET_RAW (the capability the benchmark names). cap_drop: [ALL] with only the needed cap_add satisfies both."},
			{ID: "5.5", Title: "Ensure that privileged containers are not used", Section: dockerRuntime,
				Rules: []string{"dk_privileged"}, Counts: onAttribute("privileged"), AppliesTo: dockerService,
				Guidance: "Nimbo flags services with privileged: true (the Host-level privileges rule; its network_mode and pid findings count for 5.10 and 5.16)."},
			{ID: "5.6", Title: "Ensure sensitive host system directories are not mounted on containers", Section: dockerRuntime,
				Rules: []string{"dk_host_dirs"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags bind mounts of /, /boot, /dev, /etc, /lib, /lib64, /proc, /sys and /usr (read-only ones as info), and read-write mounts of anything under them."},
			{ID: "5.7", Title: "Ensure sshd is not run within containers", Section: dockerRuntime, Manual: true, AppliesTo: dockerService,
				Guidance: "Containers should run one service and be reached with docker exec, not SSH: check no image starts or installs openssh-server, then attest."},
			{ID: "5.8", Title: "Ensure privileged ports are not mapped within containers", Section: dockerRuntime,
				Rules: []string{"dk_privileged_port"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags published host ports below 1024. The benchmark accepts 80 and 443 on a load balancer: a finding there is an accepted exception."},
			{ID: "5.9", Title: "Ensure that only needed ports are open on the container", Section: dockerRuntime, Manual: true, AppliesTo: dockerService,
				Guidance: "Publish only the ports clients outside the project use; services reach each other by name without ports:. Nimbo's Published ports match the image and Databases not published rules point at likely leftovers; attest after reviewing every ports: list."},
			{ID: "5.10", Title: "Ensure that the host's network namespace is not shared", Section: dockerRuntime,
				Rules: []string{"dk_privileged"}, Counts: onAttribute("network_mode"), AppliesTo: dockerService,
				Guidance: "Nimbo flags services with network_mode: host (the Host-level privileges rule's network_mode findings)."},
			{ID: "5.11", Title: "Ensure that the memory usage for containers is limited", Section: dockerRuntime,
				Rules: []string{"dk_no_limits"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags long-running services without a memory limit (mem_limit or deploy.resources.limits.memory); one-off jobs are skipped."},
			{ID: "5.12", Title: "Ensure that CPU priority is set appropriately on containers", Section: dockerRuntime, Manual: true, AppliesTo: dockerService,
				Guidance: "Give services cpu_shares (or deploy.resources.limits.cpus) according to their importance so one container can't starve the others; attest after sizing them."},
			{ID: "5.13", Title: "Ensure that the container's root filesystem is mounted as read only", Section: dockerRuntime,
				Rules: []string{"dk_read_only"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags services without read_only: true. Give paths the app writes a tmpfs or a volume."},
			{ID: "5.14", Title: "Ensure that incoming container traffic is bound to a specific host interface", Section: dockerRuntime,
				Rules: []string{"dk_port_interface"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags published ports without a host IP (they listen on every interface, 0.0.0.0)."},
			{ID: "5.15", Title: "Ensure that the 'on-failure' container restart policy is set to '5'", Section: dockerRuntime, Manual: true, AppliesTo: dockerService,
				Guidance: "The benchmark wants restart: on-failure:5 (or deploy.restart_policy {condition: on-failure, max_attempts: 5}) so a crash-looping container stops; Nimbo's restart advice suggests unless-stopped for availability. Decide per service and attest."},
			{ID: "5.16", Title: "Ensure that the host's process namespace is not shared", Section: dockerRuntime,
				Rules: []string{"dk_privileged"}, Counts: onAttribute("pid"), AppliesTo: dockerService,
				Guidance: "Nimbo flags services with pid: host (the Host-level privileges rule's pid findings)."},
			{ID: "5.17", Title: "Ensure that the host's IPC namespace is not shared", Section: dockerRuntime,
				Rules: []string{"dk_host_ipc"}, AppliesTo: dockerService, Guidance: "Nimbo flags services with ipc: host."},
			{ID: "5.18", Title: "Ensure that host devices are not directly exposed to containers", Section: dockerRuntime,
				Rules: []string{"dk_devices"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags every devices: mapping. A device the service really needs (a GPU, a USB adapter) is an accepted exception: map only it, with the narrowest permissions."},
			{ID: "5.19", Title: "Ensure that the default ulimit is overwritten at runtime if needed", Section: dockerRuntime, Manual: true, AppliesTo: dockerService,
				Guidance: "Set ulimits: (nofile, nproc) on services whose needs differ from the daemon's default-ulimits; attest after reviewing them."},
			{ID: "5.20", Title: "Ensure mount propagation mode is not set to shared", Section: dockerRuntime,
				Rules: []string{"dk_mount_propagation"}, AppliesTo: dockerService, Guidance: "Nimbo flags bind mounts with shared or rshared propagation."},
			{ID: "5.21", Title: "Ensure that the host's UTS namespace is not shared", Section: dockerRuntime,
				Rules: []string{"dk_host_uts"}, AppliesTo: dockerService, Guidance: "Nimbo flags services with uts: host."},
			{ID: "5.22", Title: "Ensure the default seccomp profile is not Disabled", Section: dockerRuntime,
				Rules: []string{"dk_seccomp_unconfined"}, AppliesTo: dockerService, Guidance: "Nimbo flags services with security_opt seccomp=unconfined."},
			{ID: "5.23", Title: "Ensure that docker exec commands are not used with the privileged option", Section: dockerRuntime, Manual: true,
				Guidance: "An operational practice: audit docker exec --privileged in the hosts' audit logs (auditd rule on dockerd, 1.1) and attest."},
			{ID: "5.24", Title: "Ensure that docker exec commands are not used with the user=root option", Section: dockerRuntime, Manual: true,
				Guidance: "An operational practice: audit docker exec --user root (or -u 0) in the hosts' audit logs and attest."},
			{ID: "5.25", Title: "Ensure that cgroup usage is confirmed", Section: dockerRuntime, Manual: true, AppliesTo: dockerService,
				Guidance: "Services should run in Docker's default cgroup; check any cgroup_parent: is intended, then attest."},
			{ID: "5.26", Title: "Ensure that the container is restricted from acquiring additional privileges", Section: dockerRuntime,
				Rules: []string{"dk_no_new_privileges"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags services without security_opt no-new-privileges:true. A daemon-wide no-new-privileges (2.15) also satisfies it: attest that instead if you rely on it."},
			{ID: "5.27", Title: "Ensure that container health is checked at runtime", Section: dockerRuntime,
				Rules: []string{"dk_missing_healthcheck", "dk_no_healthcheck"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags servers built here without a healthcheck, depended-on images with a known healthcheck that don't declare it, and services others wait on as healthy that have none." + dockerPulled},
			{ID: "5.28", Title: "Ensure that Docker commands always make use of the latest version of their image", Section: dockerRuntime, Manual: true, AppliesTo: dockerService,
				Guidance: "Pull before starting (docker compose pull, or pull_policy: always) so cached images don't hide newer patched ones; attest once deploys pull."},
			{ID: "5.29", Title: "Ensure that the PIDs cgroup limit is used", Section: dockerRuntime,
				Rules: []string{"dk_pids_limit"}, AppliesTo: dockerService,
				Guidance: "Nimbo flags services without pids_limit or deploy.resources.limits.pids."},
			{ID: "5.30", Title: "Ensure that Docker's default bridge \"docker0\" is not used", Section: dockerRuntime,
				Rules: []string{"dk_default_bridge"}, AppliesTo: dockerService,
				Guidance: "Compose puts services on the project's own network by default; Nimbo flags services that set network_mode: bridge."},
			{ID: "5.31", Title: "Ensure that the host's user namespaces are not shared", Section: dockerRuntime,
				Rules: []string{"dk_host_userns"}, AppliesTo: dockerService, Guidance: "Nimbo flags services with userns_mode: host."},
			{ID: "5.32", Title: "Ensure that the Docker socket is not mounted inside any containers", Section: dockerRuntime,
				Rules: []string{"dk_docker_sock"}, AppliesTo: dockerService, Guidance: "Nimbo flags bind mounts of docker.sock."},

			// 6 Docker Security Operations
			{ID: "6.1", Title: "Ensure that image sprawl is avoided", Section: dockerOps, Manual: true,
				Guidance: "Remove unused images on the hosts regularly (docker image prune, a scheduled job) and attest."},
			{ID: "6.2", Title: "Ensure that container sprawl is avoided", Section: dockerOps, Manual: true,
				Guidance: "Remove stopped containers on the hosts regularly (docker container prune, docker compose down for finished projects) and attest."},

			// 7 Docker Swarm Configuration
			{ID: "7", Title: "Docker Swarm configuration (7.1-7.9)", Section: dockerSwarm, Manual: true,
				Guidance: "Applies only to hosts in a Swarm (attest it as not used otherwise): an odd, minimal number of managers, services bound to a specific interface, encrypted overlay networks, docker secret for secrets, autolock on and its key rotated, node and CA certificates rotated, management traffic on its own network."},
		},
	})
}
