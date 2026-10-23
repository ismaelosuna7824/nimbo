package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Environments of docker workflows (the compose side of
// /workflows/{id}/environments): an override file (compose.<env>.yaml)
// and/or an env file (.env.<env> or <env>.env) next to the first compose
// file, layered like `docker compose -f compose.yaml [-f
// compose.<env>.yaml] --env-file .env --env-file <env file>`.
// Workflow.ActiveEnvironment selects the one the graph, rules, edits and
// runs use.

// readAsset reads a file of the workflow's assets (nil, nil when absent).
func (s *Server) readAsset(id, rel string) ([]byte, error) {
	root, err := os.OpenRoot(s.cfg.Store.AssetsDir(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.FromSlash(rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// assetExists reports whether a file of the workflow's assets exists.
func (s *Server) assetExists(id, rel string) bool {
	_, err := os.Stat(filepath.Join(s.cfg.Store.AssetsDir(id), filepath.FromSlash(rel)))
	return err == nil
}

// composeEnvironment resolves an environment of a docker workflow.
func composeEnvironment(wf *model.Workflow, name string) (*model.ComposeEnvironment, error) {
	if err := requireDocker(wf); err != nil {
		return nil, err
	}
	env := compose.FindEnvironment(wf.Compose, name)
	if env == nil {
		return nil, errorf(http.StatusNotFound, "environment %q not found", name)
	}
	return env, nil
}

// baseEnv parses the project's .env (empty when absent or invalid: the
// Problems tab reports its syntax errors).
func (s *Server) baseEnv(wf *model.Workflow) map[string]string {
	data, err := s.readAsset(wf.ID, compose.BaseEnvFile(compose.ProjectDir(wf.Compose.Files)))
	if err != nil || data == nil {
		return map[string]string{}
	}
	env, err := compose.ParseEnvFile(data, nil)
	if err != nil {
		return map[string]string{}
	}
	return env
}

// composeEnvironmentNames lists the environments of a docker workflow.
func composeEnvironmentNames(wf *model.Workflow) []string {
	names := make([]string, len(wf.Compose.Environments))
	for i, e := range wf.Compose.Environments {
		names[i] = e.Name
	}
	return names
}

func (s *Server) composeEnvironments(wf *model.Workflow) (model.EnvironmentsView, error) {
	if err := requireDocker(wf); err != nil {
		return model.EnvironmentsView{}, err
	}
	c := wf.Compose
	main := compose.DefaultFile
	if len(c.Files) > 0 {
		main = c.Files[0]
	}
	override := path.Base(compose.OverrideFile(c.Files, "<env>"))
	pd := compose.ProjectDir(c.Files)
	view := model.EnvironmentsView{
		Kind: model.WorkflowDocker, Active: c.Environment, Environments: []model.EnvironmentInfo{},
		Sources: []model.EnvironmentSource{{
			Label: "Compose project", Kind: "compose", Path: main, Supported: true,
			Note: override + " and/or .env.<env> (or <env>.env) next to " + path.Base(main),
		}},
		BaseFiles: slices.Clone(c.Files),
		Note: fmt.Sprintf("The base is %s and .env, shared by every environment. An environment is an override file (%s) and/or an env file (.env.<env> or <env>.env), layered like docker compose -f %s -f %s --env-file .env --env-file .env.<env>: scalars replace, mappings merge, lists append unless tagged !override.",
			strings.Join(c.Files, ", "), override, path.Base(main), override),
	}
	if s.assetExists(wf.ID, compose.BaseEnvFile(pd)) {
		view.BaseFiles = append(view.BaseFiles, compose.BaseEnvFile(pd))
	}
	_, changes := compose.Compare(compose.OpenEnvironments(context.Background(), s.cfg.Store.AssetsDir(wf.ID), c.Files, c.Profiles, compose.Options{}), composeEnvironmentNames(wf))
	base := s.baseEnv(wf)
	for _, e := range c.Environments {
		info := model.EnvironmentInfo{
			Name: e.Name, Active: e.Name == c.Environment, ProdLike: model.ProdLike(e.Name),
			Files: e.Files, Changes: changes[e.Name],
		}
		if info.Changes == nil {
			info.Changes = []model.EnvironmentChange{}
		}
		own := map[string]string{}
		if e.EnvFile != "" {
			data, err := s.readAsset(wf.ID, e.EnvFile)
			if err != nil {
				return view, err
			}
			if vars, err := compose.ParseEnvFile(data, func(k string) (string, bool) { v, ok := base[k]; return v, ok }); err == nil {
				own = vars
			}
		}
		info.Variables = compose.Variables(base, own, compose.EnvFileOf(pd, &e))
		view.Environments = append(view.Environments, info)
	}
	return view, nil
}

func (s *Server) composeCreateEnvironment(id, name, from string) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		if err := requireDocker(wf); err != nil {
			return nil, err
		}
		if err := refuseArchived(wf); err != nil {
			return nil, err
		}
		c := wf.Compose
		if len(c.Files) == 0 {
			return nil, errorf(http.StatusConflict, "the workflow has no compose file")
		}
		if compose.FindEnvironment(c, name) != nil {
			return nil, errorf(http.StatusConflict, "environment %q already exists", name)
		}
		pd := compose.ProjectDir(c.Files)
		file := compose.OverrideFile(c.Files, name)
		envFile := compose.EnvFile(pd, name)
		for _, f := range []string{file, envFile, path.Join(pd, name+".env")} {
			if s.assetExists(wf.ID, f) {
				return nil, errorf(http.StatusConflict, "%s already exists: rename or delete it first", f)
			}
		}
		files := map[string][]byte{}
		if from == "" {
			files[file] = compose.NewOverride(name, c.Files, file)
		} else {
			// A copy has what its source has: the override and/or the env
			// file (always named .env.<name>).
			src := compose.FindEnvironment(c, from)
			if src == nil {
				return nil, errorf(http.StatusNotFound, "environment %q not found", from)
			}
			if src.File != "" {
				data, err := s.readAsset(wf.ID, src.File)
				if err != nil {
					return nil, err
				}
				files[file] = compose.RenameOverride(data, from, name, src.File, file)
			}
			if src.EnvFile != "" {
				data, err := s.readAsset(wf.ID, src.EnvFile)
				if err != nil {
					return nil, err
				}
				files[envFile] = data
			}
		}
		return nil, s.writeAssets(wf.ID, files)
	})
}

func (s *Server) composeRenameEnvironment(id, name, to string) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		env, err := composeEnvironment(wf, name)
		if err != nil {
			return nil, err
		}
		if to == name {
			return nil, nil
		}
		if compose.FindEnvironment(wf.Compose, to) != nil {
			return nil, errorf(http.StatusConflict, "environment %q already exists", to)
		}
		moves := map[string]string{}
		var file string
		if env.File != "" {
			// Keep the file's spelling: docker-compose.dev.yml → docker-compose.qa.yml.
			base := path.Base(env.File)
			stem, _, _ := strings.Cut(base, ".")
			file = path.Join(path.Dir(env.File), stem+"."+to+strings.TrimPrefix(base, stem+"."+name))
			moves[env.File] = file
		}
		if env.EnvFile != "" {
			// And the env file's style: .env.dev → .env.qa, dev.env → qa.env.
			moves[env.EnvFile] = compose.RenamedEnvFile(env.EnvFile, name, to)
		}
		files := map[string][]byte{}
		for from, dst := range moves {
			if s.assetExists(wf.ID, dst) {
				return nil, errorf(http.StatusConflict, "%s already exists: rename or delete it first", dst)
			}
			data, err := s.readAsset(wf.ID, from)
			if err != nil {
				return nil, err
			}
			if from == env.File {
				data = compose.RenameOverride(data, name, to, env.File, file)
			}
			files[dst] = data
		}
		if err := s.writeAssets(wf.ID, files); err != nil {
			return nil, err
		}
		for from := range moves {
			if err := s.removeAsset(wf.ID, from); err != nil {
				return nil, err
			}
		}
		if wf.ActiveEnvironment == name {
			wf.ActiveEnvironment = to
		}
		for i := range wf.Environments {
			if wf.Environments[i].Name == name {
				wf.Environments[i].Name = to
			}
		}
		return nil, nil
	})
}

func (s *Server) composeDeleteEnvironment(id, name string) (*model.Workflow, error) {
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		env, err := composeEnvironment(wf, name)
		if err != nil {
			return nil, err
		}
		for _, f := range env.Files {
			if err := s.removeAsset(wf.ID, f); err != nil {
				return nil, err
			}
		}
		if wf.ActiveEnvironment == name {
			wf.ActiveEnvironment = ""
		}
		return nil, nil
	})
}

// composeSetEnvironmentVariable sets (value non-nil) or removes key in the
// environment's env file (its .env.<env> or <env>.env; a first variable
// creates .env.<env>), deleting the file when the last one goes (so runs
// read .env alone again; an environment that was only that file is gone).
// Secret-looking keys are secrets by name (env files have no flag), so
// secret is not stored.
func (s *Server) composeSetEnvironmentVariable(id, envName, key string, value *string, _ bool) (*model.Workflow, error) {
	if err := compose.ValidEnvKey(key); err != nil {
		return nil, editErr(err)
	}
	if value != nil && strings.ContainsAny(*value, "\x00") {
		return nil, errorf(http.StatusBadRequest, "invalid value for %s", key)
	}
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		env, err := composeEnvironment(wf, envName)
		if err != nil {
			return nil, err
		}
		if env.EnvFile == "" && value == nil {
			return nil, nil
		}
		file := compose.EnvFileOf(compose.ProjectDir(wf.Compose.Files), env)
		data, err := s.readAsset(wf.ID, file)
		if err != nil {
			return nil, err
		}
		next := compose.SetDotenv(data, key, value)
		if _, err := compose.ParseEnvFile(next, func(string) (string, bool) { return "", true }); err != nil {
			return nil, errorf(http.StatusBadRequest, "%s would not parse: %v", file, err)
		}
		if strings.TrimSpace(string(next)) == "" {
			return nil, s.removeAsset(wf.ID, file)
		}
		return nil, s.writeAssets(wf.ID, map[string][]byte{file: next})
	})
}

func (s *Server) composeCompareEnvironments(wf *model.Workflow) (model.EnvCompare, error) {
	if err := requireDocker(wf); err != nil {
		return model.EnvCompare{}, err
	}
	c := wf.Compose
	cmp, _ := compose.Compare(compose.OpenEnvironments(context.Background(), s.cfg.Store.AssetsDir(wf.ID), c.Files, c.Profiles, compose.Options{}), composeEnvironmentNames(wf))
	return cmp, nil
}

func (s *Server) composeEnvironmentSettings(wf *model.Workflow, envName, target string) (model.EnvSettings, error) {
	env, err := composeEnvironment(wf, envName)
	if err != nil {
		return model.EnvSettings{}, err
	}
	p := s.openComposeEnv(wf, envName)
	if p.Model == nil {
		return model.EnvSettings{}, errorf(http.StatusConflict, "environment %s can't be loaded: fix the compose files first", envName)
	}
	pd := compose.ProjectDir(wf.Compose.Files)
	file := env.File
	if file == "" {
		file = compose.OverrideFile(wf.Compose.Files, envName) // created by the first value
	}
	out := model.EnvSettings{
		Environment: envName, Targets: []model.EnvTarget{}, File: file,
		Note: "Values go to " + file + "; clear one to inherit the base. Variables live in " + compose.EnvFileOf(pd, env) + ".",
	}
	for _, name := range p.SortedServiceNames() {
		if !p.Inactive[name] {
			out.Targets = append(out.Targets, model.EnvTarget{ID: name, Label: name, Kind: "service"})
		}
	}
	if target == "" && len(out.Targets) > 0 {
		target = out.Targets[0].ID
	}
	out.Target = target
	if target == "" {
		out.Settings = []model.EnvSetting{}
		return out, nil
	}
	out.Settings, err = compose.Settings(s.openComposeEnv(wf, ""), p, strings.TrimPrefix(target, "service:"))
	if err != nil {
		return model.EnvSettings{}, editErr(err)
	}
	return out, nil
}

func (s *Server) composeSetEnvironmentSettings(id, envName, target string, values map[string]*string) (*model.Workflow, error) {
	svc := strings.TrimPrefix(target, "service:")
	return s.mutate(id, func(wf *model.Workflow) ([]model.Diagnostic, error) {
		env, err := composeEnvironment(wf, envName)
		if err != nil {
			return nil, err
		}
		if err := refuseArchived(wf); err != nil {
			return nil, err
		}
		// An environment that is only an env file gets its override file
		// with the first value.
		content := map[string][]byte{}
		if env.File == "" {
			setsOne := false
			for _, v := range values {
				setsOne = setsOne || v != nil
			}
			if !setsOne {
				return nil, nil
			}
			file := compose.OverrideFile(wf.Compose.Files, envName)
			content[file] = compose.NewOverride(envName, wf.Compose.Files, file)
		}
		files, err := compose.SetSettings(compose.Open(context.Background(), s.cfg.Store.AssetsDir(wf.ID), wf.Compose.Files, wf.Compose.Profiles, compose.Options{Content: content, Environment: envName}), svc, values)
		if err != nil {
			return nil, editErr(err)
		}
		// The environment must still load before anything is written.
		check := compose.Open(context.Background(), s.cfg.Store.AssetsDir(wf.ID), wf.Compose.Files, wf.Compose.Profiles, compose.Options{Content: files, Environment: envName})
		if check.Model == nil {
			for _, d := range check.Diagnostics {
				if d.Severity == model.SeverityError {
					return nil, errorf(http.StatusBadRequest, "%s", d.Message)
				}
			}
			return nil, errorf(http.StatusBadRequest, "the environment would not load")
		}
		return nil, s.writeAssets(wf.ID, files)
	})
}
