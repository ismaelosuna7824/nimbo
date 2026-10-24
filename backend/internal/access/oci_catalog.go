package access

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

//go:generate go run ./gen/oci -out data

// ociData is the pinned OCI IAM policy reference (gen/oci): verbs and the
// resource-types of the services Nimbo models.
//
//go:embed data/oci.json
var ociData []byte

type ociReference struct {
	Source model.AccessSource `json:"source"`
	Verbs  []struct {
		Name        string `json:"name"`
		Target      string `json:"target"`
		Description string `json:"description"`
	} `json:"verbs"`
	ResourceTypes []struct {
		Name    string `json:"name"`
		Service string `json:"service"`
		Family  bool   `json:"family"`
	} `json:"resourceTypes"`
}

var ociRef = sync.OnceValues(func() (*ociReference, error) {
	var r ociReference
	if err := json.Unmarshal(ociData, &r); err != nil {
		return nil, err
	}
	return &r, nil
})

func init() { RegisterCatalog("oci", ociCatalog) }

// ociCatalog serves the verbs (least to most access) and the
// resource-types (aggregate families first within each service);
// q.Search filters resource-types by name or service.
func ociCatalog(q CatalogQuery) (*model.AccessCatalog, error) {
	r, err := ociRef()
	if err != nil {
		return nil, err
	}
	out := &model.AccessCatalog{Provider: "oci", Source: r.Source, Verbs: make([]model.AccessVerb, 0, len(r.Verbs))}
	for _, v := range r.Verbs {
		out.Verbs = append(out.Verbs, model.AccessVerb{Name: v.Name, Description: v.Target + ": " + v.Description})
	}
	search := strings.ToLower(strings.TrimSpace(q.Search))
	var families, individual []model.AccessResourceType
	for _, rt := range r.ResourceTypes {
		if search != "" && !strings.Contains(rt.Name, search) && !strings.Contains(strings.ToLower(rt.Service), search) {
			continue
		}
		t := model.AccessResourceType{Name: rt.Name, Group: rt.Service, Description: "Individual resource-type"}
		if rt.Family {
			t.Description = "Aggregate resource-type: several related " + rt.Service + " resource-types in one"
			if rt.Name == "all-resources" {
				t.Description = "Every resource-type of every service"
			}
			families = append(families, t)
			continue
		}
		individual = append(individual, t)
	}
	out.ResourceTypes = append(families, individual...)
	return out, nil
}

// OCIResourceType reports whether name is a resource-type of the pinned
// reference, and whether it is an aggregate (family).
func OCIResourceType(name string) (known, family bool) {
	r, err := ociRef()
	if err != nil {
		return false, false
	}
	for _, rt := range r.ResourceTypes {
		if rt.Name == name {
			return true, rt.Family
		}
	}
	return false, false
}
