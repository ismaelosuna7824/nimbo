// Command gen writes the pinned Kubernetes OpenAPI definitions the k8s_schema
// rule validates against (and GET /catalog/kubernetes/schema serves): the
// swagger.json of a Kubernetes release, reduced to the definitions, the
// schema keywords the validator and the editor read, and the namespaced
// kinds.
//
//	go run ./gen -version 1.36.5 -out schema
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// kept are the schema keywords of the Kubernetes swagger the validator and
// the editor use (patch strategies, unions and map types are dropped).
var kept = map[string]bool{
	"type": true, "format": true, "$ref": true, "properties": true, "items": true, "required": true,
	"additionalProperties": true, "description": true, "x-kubernetes-group-version-kind": true,
	"x-kubernetes-list-type": true, "x-kubernetes-list-map-keys": true,
}

func main() {
	version := flag.String("version", "1.36.5", "Kubernetes release (major.minor.patch)")
	out := flag.String("out", "schema", "output directory")
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
		Definitions map[string]map[string]any `json:"definitions"`
		Paths       map[string]map[string]any `json:"paths"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil {
		log.Fatal(err)
	}
	defs := map[string]any{}
	for name, def := range spec.Definitions {
		// DeleteOptions and WatchEvent name every group/version: they are
		// not the schema of those kinds.
		if gvk, ok := def["x-kubernetes-group-version-kind"].([]any); ok && len(gvk) != 1 {
			delete(def, "x-kubernetes-group-version-kind")
		}
		defs[name] = strip(def)
	}
	// Namespaced kinds: those created (post) under /namespaces/{namespace}/
	// (cluster-scoped kinds are not).
	namespaced := map[string]bool{}
	for p, ops := range spec.Paths {
		_, rest, ok := strings.Cut(p, "/namespaces/{namespace}/")
		if !ok || strings.Contains(rest, "/") {
			continue // not namespaced, or a subresource (pods/{name}/eviction…)
		}
		post, _ := ops["post"].(map[string]any)
		gvk, _ := post["x-kubernetes-group-version-kind"].(map[string]any)
		if gvk == nil {
			continue
		}
		gv := fmt.Sprint(gvk["version"])
		if g := fmt.Sprint(gvk["group"]); g != "" {
			gv = g + "/" + gv
		}
		namespaced[gv+"/"+fmt.Sprint(gvk["kind"])] = true
	}
	ns := slices.Sorted(maps.Keys(namespaced))

	minor := strings.Join(strings.Split(*version, ".")[:2], ".")
	raw, err := json.Marshal(map[string]any{"version": *version, "namespaced": ns, "definitions": defs})
	if err != nil {
		log.Fatal(err)
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := io.Copy(zw, bytes.NewReader(raw)); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	file := filepath.Join(*out, "kubernetes-"+minor+".json.gz")
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %d definitions, %d namespaced kinds, %d bytes\n", file, len(defs), len(ns), buf.Len())
}

// strip keeps the kept keywords of a schema (recursively).
func strip(s map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range s {
		if !kept[k] {
			continue
		}
		switch k {
		case "properties":
			props := map[string]any{}
			for name, p := range v.(map[string]any) {
				props[name] = strip(p.(map[string]any))
			}
			out[k] = props
		case "items", "additionalProperties":
			if m, ok := v.(map[string]any); ok {
				out[k] = strip(m)
			} else {
				out[k] = v
			}
		default:
			out[k] = v
		}
	}
	return out
}
