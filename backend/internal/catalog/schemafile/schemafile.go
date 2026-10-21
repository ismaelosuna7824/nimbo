// Package schemafile defines the format of the generated, embedded provider
// schema files shared by the catalog and its generator.
package schemafile

import "github.com/ismaelosuna7824/nimbo/backend/internal/model"

// File is one provider's trimmed schema (data/<name>.json.gz). Provider is
// the schema of the provider configuration block (`provider "aws" {}`).
type File struct {
	Name      string           `json:"name"`
	Source    string           `json:"source"`
	Version   string           `json:"version"`
	Provider  *Entry           `json:"provider,omitempty"`
	Resources map[string]Entry `json:"resources"`
	Data      map[string]Entry `json:"data"`
}

// Entry is the schema of one resource or data source type.
type Entry struct {
	Description string            `json:"description,omitempty"`
	Block       model.SchemaBlock `json:"block"`
}

// Functions lists Terraform's built-in functions (data/functions.json.gz,
// from `terraform metadata functions -json`), sorted by name.
type Functions struct {
	Terraform string                    `json:"terraform"` // version that reported them
	Functions []model.FunctionSignature `json:"functions"`
}

// Index lists every provider's version and type names (data/index.json.gz)
// so the catalog can search without decompressing the full schemas.
type Index struct {
	Providers []IndexProvider `json:"providers"`
}

// IndexProvider is one provider entry of the Index; type names are sorted.
type IndexProvider struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Version   string   `json:"version"`
	Resources []string `json:"resources"`
	Data      []string `json:"data"`
}
