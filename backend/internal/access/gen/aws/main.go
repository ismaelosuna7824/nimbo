// Command aws regenerates ../../data/aws.json.gz, the AWS Service
// Authorization Reference pinned in the daemon: every service prefix with
// its name, actions (access level from the reference's annotations) and
// resource types (ARN formats). Run it from the backend module with
// `go generate ./internal/access/...` (or `go run ./internal/access/gen/aws
// -out internal/access/data`).
//
// Sources (both official AWS documentation):
//   - machine-readable service reference: https://servicereference.us-east-1.amazonaws.com/
//     (https://docs.aws.amazon.com/service-authorization/latest/reference/service-reference.html)
//   - service names: the Service Authorization Reference table of contents
//     (https://docs.aws.amazon.com/service-authorization/latest/reference/toc-contents.json),
//     whose titles read "Amazon DynamoDB (dynamodb)".
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	indexURL = "https://servicereference.us-east-1.amazonaws.com/"
	tocURL   = "https://docs.aws.amazon.com/service-authorization/latest/reference/toc-contents.json"
	docURL   = "https://docs.aws.amazon.com/service-authorization/latest/reference/service-reference.html"
)

// File is the generated data file (read by package access).
type File struct {
	Source   Source    `json:"source"`
	Services []Service `json:"services"`
}

// Source cites the data.
type Source struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Retrieved string `json:"retrieved"`
}

// Service is one IAM service prefix.
type Service struct {
	Prefix    string     `json:"prefix"`
	Name      string     `json:"name"`
	Version   string     `json:"version,omitempty"`
	Resources []Resource `json:"resources,omitempty"`
	Actions   []Action   `json:"actions"`
}

// Resource is a resource type with its ARN formats.
type Resource struct {
	Name string   `json:"name"`
	ARNs []string `json:"arns,omitempty"`
}

// Action is an action with its access level and resource types.
type Action struct {
	Name      string   `json:"name"`
	Level     string   `json:"level"`
	Resources []string `json:"resources,omitempty"`
}

type indexEntry struct {
	Service string `json:"service"`
	URL     string `json:"url"`
}

type serviceDoc struct {
	Name    string `json:"Name"`
	Version string `json:"Version"`
	Actions []struct {
		Name        string `json:"Name"`
		Annotations struct {
			Properties struct {
				IsList                 bool `json:"IsList"`
				IsPermissionManagement bool `json:"IsPermissionManagement"`
				IsTaggingOnly          bool `json:"IsTaggingOnly"`
				IsWrite                bool `json:"IsWrite"`
			} `json:"Properties"`
		} `json:"Annotations"`
		Resources []struct {
			Name string `json:"Name"`
		} `json:"Resources"`
	} `json:"Actions"`
	Resources []struct {
		Name       string   `json:"Name"`
		ARNFormats []string `json:"ARNFormats"`
	} `json:"Resources"`
}

type tocNode struct {
	Title    string    `json:"title"`
	Href     string    `json:"href"`
	Contents []tocNode `json:"contents"`
}

func main() {
	out := flag.String("out", "data", "output directory")
	flag.Parse()
	log.SetFlags(0)
	if err := run(*out); err != nil {
		log.Fatal(err)
	}
}

var client = &http.Client{Timeout: 60 * time.Second}

func get(url string, v any) error {
	var lastErr error
	for attempt := range 4 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GET %s: %s", url, resp.Status)
			continue
		}
		return json.Unmarshal(body, v)
	}
	return lastErr
}

// level maps the reference's annotations to the access levels of the
// Service Authorization Reference tables.
func level(list, perm, tag, write bool) string {
	switch {
	case list:
		return "List"
	case perm:
		return "Permissions management"
	case tag:
		return "Tagging"
	case write:
		return "Write"
	}
	return "Read"
}

var titleRe = regexp.MustCompile(`^(.*\S)\s*\(([a-z0-9][a-z0-9-]*)\)$`)

// serviceNames maps prefixes to the titles of their reference pages. A
// prefix shared by several pages lists the shortest title first.
func serviceNames() (map[string]string, error) {
	var toc struct {
		Contents []tocNode `json:"contents"`
	}
	if err := get(tocURL, &toc); err != nil {
		return nil, err
	}
	all := map[string][]string{}
	var walk func(ns []tocNode)
	walk = func(ns []tocNode) {
		for _, n := range ns {
			if m := titleRe.FindStringSubmatch(n.Title); m != nil && strings.HasPrefix(n.Href, "list_") {
				if !slices.Contains(all[m[2]], m[1]) {
					all[m[2]] = append(all[m[2]], m[1])
				}
			}
			walk(n.Contents)
		}
	}
	walk(toc.Contents)
	names := map[string]string{}
	for p, ts := range all {
		slices.SortStableFunc(ts, func(a, b string) int {
			if len(a) != len(b) {
				return len(a) - len(b)
			}
			return strings.Compare(a, b)
		})
		names[p] = strings.Join(ts, " / ")
	}
	return names, nil
}

func run(out string) error {
	var index []indexEntry
	if err := get(indexURL, &index); err != nil {
		return err
	}
	names, err := serviceNames()
	if err != nil {
		return err
	}
	services := make([]Service, len(index))
	errs := make([]error, len(index))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, e := range index {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var doc serviceDoc
			if err := get(e.URL, &doc); err != nil {
				errs[i] = fmt.Errorf("%s: %w", e.Service, err)
				return
			}
			s := Service{Prefix: e.Service, Name: names[e.Service], Version: doc.Version, Actions: []Action{}}
			if s.Name == "" {
				s.Name = e.Service
			}
			for _, r := range doc.Resources {
				s.Resources = append(s.Resources, Resource{Name: r.Name, ARNs: r.ARNFormats})
			}
			for _, a := range doc.Actions {
				p := a.Annotations.Properties
				act := Action{Name: a.Name, Level: level(p.IsList, p.IsPermissionManagement, p.IsTaggingOnly, p.IsWrite)}
				for _, r := range a.Resources {
					act.Resources = append(act.Resources, r.Name)
				}
				s.Actions = append(s.Actions, act)
			}
			slices.SortFunc(s.Actions, func(a, b Action) int { return strings.Compare(a.Name, b.Name) })
			services[i] = s
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	slices.SortFunc(services, func(a, b Service) int { return strings.Compare(a.Prefix, b.Prefix) })
	f := File{
		Source:   Source{Title: "AWS Service Authorization Reference", URL: docURL, Retrieved: time.Now().UTC().Format("2006-01-02")},
		Services: services,
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(raw); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	path := filepath.Join(out, "aws.json.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	actions := 0
	for _, s := range services {
		actions += len(s.Actions)
	}
	log.Printf("wrote %s: %d services, %d actions (%d bytes)", path, len(services), actions, buf.Len())
	return nil
}
