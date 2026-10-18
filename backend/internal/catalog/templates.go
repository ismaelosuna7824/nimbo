package catalog

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Each templates/<id>/ directory holds root-module *.tf files; metadata
// lives in templates/index.json.
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

// TemplateFiles returns the Terraform files of a template (file name ->
// content) for hclcodec.Import.
func (c *Catalog) TemplateFiles(id string) (map[string][]byte, bool) {
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return nil, false
	}
	dir := path.Join("templates", id)
	entries, err := fs.ReadDir(templatesFS, dir)
	if err != nil {
		return nil, false
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		b, err := templatesFS.ReadFile(path.Join(dir, e.Name()))
		if err != nil {
			return nil, false
		}
		files[e.Name()] = b
	}
	return files, len(files) > 0
}
