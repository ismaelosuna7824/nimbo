package compose

import "github.com/compose-spec/compose-go/v2/types"

// Kinds of Reference.
const (
	RefCompose    = "compose file" // a listed, included or extended compose file
	RefContext    = "build context"
	RefDockerfile = "Dockerfile"
	RefEnvFile    = "env_file"
	RefBind       = "bind mount"
	RefConfig     = "config"
	RefSecret     = "secret"
)

// Reference is a host path the compose project reads: what an import must
// bring along for the project to build and run.
type Reference struct {
	Path    string // slash path relative to Dir; "../x" when outside it
	Kind    string
	Service string // the service using it; "" for compose files, configs and secrets
	// Dockerfile is, for a build context, the Dockerfile built from it
	// (relative to Dir; "" when inline or remote).
	Dockerfile string
}

// References lists the compose files read and, when the project loads,
// every local build context, Dockerfile, env_file, bind mount source and
// config/secret file, after interpolation. Remote contexts and inline
// Dockerfiles are left out.
func (p *Project) References() []Reference {
	out := make([]Reference, 0, len(p.Order))
	for _, f := range p.Order {
		out = append(out, Reference{Path: f, Kind: RefCompose})
	}
	if p.Model == nil {
		return out
	}
	for _, name := range p.SortedServiceNames() {
		s := p.Model.Services[name]
		if ctx := p.contextPath(s); ctx != "" {
			df := p.dockerfilePath(s)
			out = append(out, Reference{Path: ctx, Kind: RefContext, Service: name, Dockerfile: df})
			if df != "" {
				out = append(out, Reference{Path: df, Kind: RefDockerfile, Service: name})
			}
		}
		for _, ef := range s.EnvFiles {
			out = append(out, Reference{Path: p.rel(ef.Path), Kind: RefEnvFile, Service: name})
		}
		for _, m := range s.Volumes {
			if m.Type == types.VolumeTypeBind && m.Source != "" {
				out = append(out, Reference{Path: p.rel(m.Source), Kind: RefBind, Service: name})
			}
		}
	}
	for _, name := range sortedKeys(p.Model.Configs) {
		if f := p.Model.Configs[name].File; f != "" {
			out = append(out, Reference{Path: p.rel(f), Kind: RefConfig})
		}
	}
	for _, name := range sortedKeys(p.Model.Secrets) {
		if f := p.Model.Secrets[name].File; f != "" {
			out = append(out, Reference{Path: p.rel(f), Kind: RefSecret})
		}
	}
	return out
}
