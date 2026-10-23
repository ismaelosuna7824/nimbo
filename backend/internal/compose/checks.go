package compose

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// check runs the semantic checks on the effective model: references,
// port clashes, build contexts and Dockerfiles, env files, and the advice
// (latest tags, healthchecks, literal secrets, root user).
func (p *Project) check() {
	if p.Model == nil {
		return
	}
	m := p.Model
	names := p.SortedServiceNames()
	type hostPort struct{ ip, port, proto string }
	published := map[hostPort]string{}

	for _, name := range names {
		s := m.Services[name]
		id := ServiceID(name)
		ref := func(field, what, target string) {
			file, line := p.refLine(name, field, target)
			p.diag(model.SeverityError, "dk_unknown_ref", file, line, id,
				fmt.Sprintf("service %s: %s %q is not declared", name, what, target),
				fmt.Sprintf("Declare %q or remove the reference from %s.", target, field))
		}
		for _, dep := range sortedKeys(s.DependsOn) {
			if _, ok := m.Services[dep]; !ok {
				ref("depends_on", "depends on service", dep)
			}
		}
		for _, l := range s.Links {
			t, _, _ := strings.Cut(l, ":")
			if _, ok := m.Services[t]; !ok {
				ref("links", "links to service", t)
			}
		}
		for _, v := range s.VolumesFrom {
			t := strings.TrimSuffix(strings.TrimSuffix(v, ":ro"), ":rw")
			if strings.HasPrefix(t, "container:") {
				continue
			}
			if _, ok := m.Services[t]; !ok {
				ref("volumes_from", "takes volumes from service", t)
			}
		}
		if t, ok := strings.CutPrefix(s.NetworkMode, "service:"); ok {
			if _, ok := m.Services[t]; !ok {
				ref("network_mode", "shares the network of service", t)
			}
		}
		for _, nw := range sortedKeys(s.Networks) {
			if _, ok := m.Networks[nw]; !ok && nw != "default" {
				ref("networks", "joins network", nw)
			}
		}
		for _, v := range s.Volumes {
			if v.Type == types.VolumeTypeVolume && v.Source != "" {
				if _, ok := m.Volumes[v.Source]; !ok {
					ref("volumes", "mounts volume", v.Source)
				}
			}
		}
		for _, c := range s.Configs {
			if _, ok := m.Configs[c.Source]; !ok {
				ref("configs", "uses config", c.Source)
			}
		}
		for _, sc := range s.Secrets {
			if _, ok := m.Secrets[sc.Source]; !ok {
				ref("secrets", "uses secret", sc.Source)
			}
		}
		if s.Image == "" && s.Build == nil {
			file, line := p.ServiceLine(name)
			p.diag(model.SeverityError, "dk_schema", file, line, id, fmt.Sprintf("service %s has neither image nor build", name), "Set image: (e.g. nginx:1.27-alpine) or build: (a folder with a Dockerfile).")
		}

		// Host ports (active services only: inactive ones don't start).
		if replicas := s.GetScale(); replicas > 1 && !p.Inactive[name] {
			for _, port := range s.Ports {
				if port.Published != "" && !strings.Contains(port.Published, "-") {
					file, line := p.fieldLine(name, "ports")
					p.diag(model.SeverityError, "dk_port_clash", file, line, id,
						fmt.Sprintf("service %s runs %d replicas but publishes the fixed host port %s: only one replica can bind it", name, replicas, port.Published),
						fmt.Sprintf("Publish a range (\"%s-%d:%d\"), only the container port (\"%d\"), or run one replica.", port.Published, atoi(port.Published)+replicas-1, port.Target, port.Target))
					break
				}
			}
		}
		if !p.Inactive[name] {
			for _, port := range s.Ports {
				if port.Published == "" {
					continue
				}
				proto := port.Protocol
				if proto == "" {
					proto = "tcp"
				}
				for _, hp := range expandRange(port.Published) {
					k := hostPort{port.HostIP, hp, proto}
					other, clash := published[k]
					if !clash {
						// 0.0.0.0 clashes with any specific IP and vice versa.
						for kk, o := range published {
							if kk.port == hp && kk.proto == proto && (kk.ip == "" || k.ip == "" || kk.ip == "0.0.0.0" || k.ip == "0.0.0.0") && o != name {
								other, clash = o, true
							}
						}
					}
					if clash && other != name {
						file, line := p.fieldLine(name, "ports")
						p.diag(model.SeverityError, "dk_port_clash", file, line, id,
							fmt.Sprintf("services %s and %s both publish host port %s/%s", other, name, hp, proto),
							fmt.Sprintf("Publish %s on another host port (e.g. \"%s:%d\").", name, nextPort(hp), port.Target))
						continue
					}
					published[k] = name
				}
			}
		}

		// Build context and Dockerfile.
		if s.Build != nil {
			ctx := p.contextPath(s)
			file, line := p.fieldLine(name, "build")
			switch {
			case ctx == "":
			case strings.HasPrefix(ctx, ".."):
				p.diag(model.SeverityError, "dk_missing_context", file, line, id, fmt.Sprintf("service %s: build context %s is outside the workflow files", name, ctx), "Copy the sources into the workflow and point build.context at that folder.")
			case !p.isDir(ctx):
				p.diag(model.SeverityError, "dk_missing_context", file, line, id, fmt.Sprintf("service %s: build context %s does not exist", name, ctx), fmt.Sprintf("Create the folder %s with a Dockerfile, or fix build.context.", ctx))
			default:
				if df := p.dockerfilePath(s); df != "" {
					if _, err := p.read(df); err != nil {
						p.diag(model.SeverityError, "dk_missing_dockerfile", file, line, id, fmt.Sprintf("service %s: %s does not exist", name, df), fmt.Sprintf("Create %s, or set build.dockerfile to the Dockerfile to use.", df))
					}
				}
			}
		}

		// env_file.
		for _, ef := range s.EnvFiles {
			rel := p.rel(ef.Path)
			if _, err := p.read(rel); err != nil {
				file, line := p.fieldLine(name, "env_file")
				sev := model.SeverityWarning
				if bool(ef.Required) {
					sev = model.SeverityError
				}
				p.diag(sev, "dk_missing_env_file", file, line, id, fmt.Sprintf("service %s: env_file %s does not exist", name, rel), fmt.Sprintf("Create %s or remove it from env_file.", rel))
			}
		}

		// Advice.
		if s.Build == nil && s.Image != "" {
			if img := ParseImage(s.Image); img.Digest == "" && (img.Tag == "" || img.Tag == "latest") && !strings.Contains(img.Repo, "$") {
				file, line := p.fieldLine(name, "image")
				p.diag(model.SeverityInfo, "dk_latest_tag", file, line, id, fmt.Sprintf("service %s uses %s without a version tag", name, s.Image), fmt.Sprintf("Pin a version, e.g. %s:<version> (see the image's tags on Docker Hub).", img.Repo))
			}
		}
		for _, dep := range sortedKeys(s.DependsOn) {
			if s.DependsOn[dep].Condition != types.ServiceConditionHealthy {
				continue
			}
			target, ok := m.Services[dep]
			if !ok || p.hasHealthcheck(target) {
				continue
			}
			file, line := p.fieldLine(name, "depends_on", dep)
			p.diag(model.SeverityWarning, "dk_no_healthcheck", file, line, ServiceID(dep),
				fmt.Sprintf("%s waits for %s to be healthy, but %s has no healthcheck", name, dep, dep),
				fmt.Sprintf("Add a healthcheck to %s (e.g. test: [\"CMD-SHELL\", \"pg_isready -U postgres\"] for PostgreSQL), or use condition: service_started.", dep))
		}
		p.checkLiteralSecrets(name)
		if s.Build != nil && s.User == "" {
			if final := p.finalStage(s); final != nil {
				u := strings.TrimSpace(final.User)
				if u == "" || u == "root" || u == "0" || strings.HasPrefix(u, "root:") || strings.HasPrefix(u, "0:") {
					df := p.dockerfilePath(s)
					p.diag(model.SeverityInfo, "dk_root_user", df, final.Line, id,
						fmt.Sprintf("service %s runs as root: its Dockerfile's final stage has no USER", name),
						"Add a non-root user to the final stage, e.g. RUN adduser -D app then USER app (or set user: in the service).")
				}
			}
		}
	}
	p.practices()
}

func (p *Project) isDir(rel string) bool {
	if rel == "." {
		return true
	}
	fi, err := os.Stat(filepath.Join(p.Dir, filepath.FromSlash(rel)))
	return err == nil && fi.IsDir() && fs.ValidPath(rel)
}

// hasHealthcheck: a compose healthcheck or the Dockerfile's HEALTHCHECK.
// Images pulled as is may have one too; only those built here are known.
func (p *Project) hasHealthcheck(s types.ServiceConfig) bool {
	if s.HealthCheck != nil {
		return !s.HealthCheck.Disable && (len(s.HealthCheck.Test) == 0 || s.HealthCheck.Test[0] != "NONE")
	}
	if s.Build != nil {
		if final := p.finalStage(s); final != nil && final.Health != nil {
			return !final.Health.Disabled
		}
	}
	return false
}

// checkLiteralSecrets flags credentials written literally in a service's
// environment (not ${VAR} from .env).
func (p *Project) checkLiteralSecrets(name string) {
	for _, rel := range p.Order {
		_, sv := p.Docs[rel].Get("services", name)
		if sv == nil {
			continue
		}
		env := mergedGet(sv, "environment")
		if env == nil || env.Kind == yaml.AliasNode {
			continue // shared through an anchor: reported where it is written
		}
		for _, k := range sortedKeys(envEntries(env)) {
			e := envEntries(env)[k]
			if !IsSecretKey(k) || !e.set || strings.TrimSpace(e.value) == "" || varRefRe.MatchString(e.value) {
				continue
			}
			p.diag(model.SeverityWarning, "dk_secret_env", rel, e.node.Line, ServiceID(name),
				fmt.Sprintf("service %s: %s is written in %s", name, k, rel),
				fmt.Sprintf("Move the value to .env (%s=…) and write %s: ${%s}, or use secrets:.", k, k, k))
		}
	}
}

// expandRange lists the ports of "8000-8002" (or one port).
func expandRange(s string) []string {
	lo, hi, ok := strings.Cut(s, "-")
	if !ok {
		return []string{s}
	}
	a, err1 := strconv.Atoi(lo)
	b, err2 := strconv.Atoi(hi)
	if err1 != nil || err2 != nil || b < a || b-a > 1000 {
		return []string{s}
	}
	out := make([]string, 0, b-a+1)
	for i := a; i <= b; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func nextPort(p string) string {
	n, err := strconv.Atoi(p)
	if err != nil {
		return p
	}
	return strconv.Itoa(n + 1)
}
