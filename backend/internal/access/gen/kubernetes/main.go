// Command kubernetes writes the pinned Kubernetes RBAC reference data the
// RBAC editors and grants read (backend/internal/kube/rbac/resources.json):
// every built-in resource and subresource of a Kubernetes release with its
// API group, kind, scope and the request verbs its endpoints serve, derived
// from the release's OpenAPI v2 document (api/openapi-spec/swagger.json).
//
//	go run ./internal/access/gen/kubernetes -version 1.36.5 -out internal/kube/rbac/resources.json
//
// Request verbs follow "Determine the request verb"
// (https://kubernetes.io/docs/reference/access-authn-authz/authorization/#determine-the-request-verb):
// POST is create, GET/HEAD get (list on collections, watch with ?watch),
// PUT update, PATCH patch, DELETE delete (deletecollection on collections).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

type resource struct {
	Group      string   `json:"group"`
	Resource   string   `json:"resource"`
	Kind       string   `json:"kind,omitempty"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs"`
}

type source struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Retrieved string `json:"retrieved"`
}

type operation struct {
	Action string `json:"x-kubernetes-action"`
	GVK    *struct {
		Group string `json:"group"`
		Kind  string `json:"kind"`
	} `json:"x-kubernetes-group-version-kind"`
}

func main() {
	version := flag.String("version", "1.36.5", "Kubernetes release (major.minor.patch)")
	out := flag.String("out", "internal/kube/rbac/resources.json", "output file")
	flag.Parse()

	url := "https://raw.githubusercontent.com/kubernetes/kubernetes/v" + *version + "/api/openapi-spec/swagger.json"
	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("GET %s: %s", url, resp.Status)
	}
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		log.Fatal(err)
	}

	byKey := map[string]*resource{}
	verbs := map[string]map[string]bool{}
	for p, ops := range spec.Paths {
		group, rest, ok := splitPath(p)
		if !ok {
			continue
		}
		watch := false
		if len(rest) > 0 && rest[0] == "watch" {
			watch, rest = true, rest[1:]
		}
		namespaced := false
		if len(rest) >= 3 && rest[0] == "namespaces" && rest[1] == "{namespace}" {
			namespaced, rest = true, rest[2:]
		}
		if len(rest) == 0 || strings.HasPrefix(rest[0], "{") {
			continue
		}
		name := rest[0]
		collection := len(rest) == 1
		if len(rest) >= 3 {
			name += "/" + rest[2] // subresource: pods/log, deployments/scale
			collection = false
		}
		key := group + "|" + name
		r := byKey[key]
		if r == nil {
			r = &resource{Group: group, Resource: name}
			byKey[key] = r
			verbs[key] = map[string]bool{}
		}
		r.Namespaced = r.Namespaced || namespaced
		for method, raw := range ops {
			var op operation
			if json.Unmarshal(raw, &op) != nil || op.Action == "" && method == "parameters" {
				continue
			}
			if r.Kind == "" && op.GVK != nil && !strings.Contains(name, "/") {
				r.Kind = op.GVK.Kind
			}
			if v := verb(method, op.Action, collection, watch); v != "" {
				verbs[key][v] = true
			}
		}
	}
	var list []resource
	for _, key := range slices.Sorted(maps.Keys(byKey)) {
		r := byKey[key]
		r.Verbs = slices.Sorted(maps.Keys(verbs[key]))
		if len(r.Verbs) == 0 {
			continue
		}
		list = append(list, *r)
	}

	src := source{
		Title:     "Kubernetes v" + *version + " API (OpenAPI swagger.json) and RBAC request verbs",
		URL:       url,
		Retrieved: time.Now().Format("2006-01-02"),
	}
	var b bytes.Buffer
	head, _ := json.Marshal(src)
	fmt.Fprintf(&b, "{\n\"version\": %q,\n\"source\": %s,\n\"resources\": [\n", *version, head)
	for i, r := range list {
		line, _ := json.Marshal(r)
		b.Write(line)
		if i < len(list)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n}\n")
	if err := os.WriteFile(*out, b.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %d resources, %d bytes\n", *out, len(list), b.Len())
}

// splitPath splits an API path into its group ("" for the core group) and
// the segments after the version.
func splitPath(p string) (string, []string, bool) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case len(parts) >= 3 && parts[0] == "api" && parts[1] == "v1":
		return "", parts[2:], true
	case len(parts) >= 4 && parts[0] == "apis":
		return parts[1], parts[3:], true
	}
	return "", nil, false
}

// verb is the RBAC request verb of an operation.
func verb(method, action string, collection, watch bool) string {
	switch action {
	case "watch", "watchlist":
		return "watch"
	case "list":
		return "list"
	case "deletecollection":
		return "deletecollection"
	}
	switch method {
	case "get", "head":
		switch {
		case watch:
			return "watch"
		case collection:
			return "list"
		}
		return "get"
	case "post":
		return "create"
	case "put":
		return "update"
	case "patch":
		return "patch"
	case "delete":
		if collection {
			return "deletecollection"
		}
		return "delete"
	}
	return ""
}
