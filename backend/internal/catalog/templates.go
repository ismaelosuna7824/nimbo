package catalog

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Each templates/<id>/ directory holds root-module *.tf files plus the
// template's asset files in subfolders (application sources such as
// services/<svc>/src/index.mjs, read by the Terraform through
// path.module); metadata lives in templates/index.json.
//
//go:embed templates
var templatesFS embed.FS

// Templates lists the starter templates in display order.
func (c *Catalog) Templates() []model.Template {
	raw, err := templatesFS.ReadFile("templates/index.json")
	if err != nil {
		return nil
	}
	var ts []model.Template
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil
	}
	return ts
}

// TemplateFiles returns the files of a template (slash path relative to the
// template folder -> content): the root-level *.tf files, which
// hclcodec.Import reads, and the asset files in subfolders, which belong in
// the new workflow's assets directory.
func (c *Catalog) TemplateFiles(id string) (map[string][]byte, bool) {
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return nil, false
	}
	dir := path.Join("templates", id)
	files := map[string][]byte{}
	tf := 0
	err := fs.WalkDir(templatesFS, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := templatesFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if !strings.Contains(rel, "/") && strings.HasSuffix(rel, ".tf") {
			tf++
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, false
	}
	return files, tf > 0
}
