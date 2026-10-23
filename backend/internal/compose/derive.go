package compose

import (
	"context"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Derive reads the docker workflow wf's files in dir and refreshes
// wf.Compose (everything but Files and Profiles) for its active
// environment, and wf.Environments (one per override file; see
// Environments), and returns the diagnostics; warning/info findings of
// rules disabled in cfg are left out. When the files can't be loaded the
// last graph is kept so the canvas does not empty while the user fixes a
// typo.
func Derive(ctx context.Context, dir string, wf *model.Workflow, cfg model.BestPracticeSettings) []model.Diagnostic {
	if wf.Compose == nil {
		wf.Compose = &model.ComposeModel{}
	}
	c := wf.Compose
	if c.Files == nil {
		c.Files = []string{}
	}
	c.Project = model.ComposeProjectName(wf.ID)
	p := Open(ctx, dir, c.Files, c.Profiles, Options{})
	c.Environments, c.Environment = p.Environments, ""
	if wf.ActiveEnvironment != "" && FindEnvironment(c, wf.ActiveEnvironment) != nil {
		p = Open(ctx, dir, c.Files, c.Profiles, Options{Environment: wf.ActiveEnvironment})
		c.Environment = p.Environment
	}
	c.Name = p.Name
	c.Included = nil
	for _, rel := range p.Order {
		if !slices.Contains(c.Files, rel) && rel != p.Override {
			c.Included = append(c.Included, rel)
		}
	}
	c.Dockerfiles = sortedKeys(p.Dockerfiles)
	c.Unresolved = p.Unresolved
	if p.Model != nil {
		c.Graph = p.Graph(c.Graph.Nodes)
	}
	if c.Graph.Nodes == nil {
		c.Graph.Nodes = []model.ComposeNode{}
	}
	if c.Graph.Edges == nil {
		c.Graph.Edges = []model.ComposeEdge{}
	}
	// Environments follow the override files (values kept by name).
	byName := map[string]model.Environment{}
	for _, e := range wf.Environments {
		byName[e.Name] = e
	}
	wf.Environments = []model.Environment{}
	for _, env := range c.Environments {
		e, ok := byName[env.Name]
		if !ok {
			e = model.Environment{Name: env.Name, Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}}
		}
		wf.Environments = append(wf.Environments, e)
	}
	return FilterSettings(p.Diagnostics, cfg)
}

// FileKind classifies a file of a docker workflow for the Code tab.
func FileKind(wf *model.Workflow, rel string) model.FileKind {
	base := rel[strings.LastIndex(rel, "/")+1:]
	switch {
	case wf.Compose != nil && (slices.Contains(wf.Compose.Files, rel) || slices.Contains(wf.Compose.Included, rel)):
		return model.FileCompose
	case wf.Compose != nil && slices.Contains(wf.Compose.Dockerfiles, rel), IsDockerfileName(base):
		return model.FileDockerfile
	case base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env"):
		return model.FileEnv
	case IsComposeName(base) && !strings.Contains(rel, "/"):
		return model.FileCompose
	}
	return model.FileAsset
}
