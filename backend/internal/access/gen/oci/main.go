// Command oci regenerates ../../data/oci.json, the OCI IAM policy reference
// pinned in the daemon: the four verbs (from Oracle's "Verbs" page) and the
// resource-types (aggregate families and individual types) of the services
// Nimbo models, read from the "Resource-Types" section of each service's
// page of the Detailed Service Policy Reference. Run it from the backend
// module with `go generate ./internal/access/...` (or `go run
// ./internal/access/gen/oci -out internal/access/data`).
//
// Sources (official Oracle Cloud Infrastructure documentation):
//   - https://docs.oracle.com/en-us/iaas/Content/Identity/policyreference/policyreference.htm
//   - https://docs.oracle.com/en-us/iaas/Content/Identity/policieshow/Verbs.htm
//   - https://docs.oracle.com/en-us/iaas/Content/Identity/policiesgs/policies_topic-ResourceTypes.htm (all-resources)
//   - the service pages listed in pages below.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	referenceURL = "https://docs.oracle.com/en-us/iaas/Content/Identity/policyreference/policyreference.htm"
	verbsURL     = "https://docs.oracle.com/en-us/iaas/Content/Identity/policieshow/Verbs.htm"
	resourcesURL = "https://docs.oracle.com/en-us/iaas/Content/Identity/policiesgs/policies_topic-ResourceTypes.htm"
	policyRef    = "https://docs.oracle.com/en-us/iaas/Content/Identity/policyreference/"
)

// File is the generated data file (read by package access).
type File struct {
	Source        Source         `json:"source"`
	Verbs         []Verb         `json:"verbs"`
	ResourceTypes []ResourceType `json:"resourceTypes"`
}

// Source cites the data.
type Source struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Retrieved string `json:"retrieved"`
}

// Verb is one policy verb with Oracle's description of the access it covers.
type Verb struct {
	Name        string `json:"name"`
	Target      string `json:"target"`
	Description string `json:"description"`
}

// ResourceType is an aggregate (family) or individual resource-type.
type ResourceType struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	Family  bool   `json:"family,omitempty"`
	URL     string `json:"url"`
}

// page is one service page of the policy reference.
type page struct {
	service, url string
}

var pages = []page{
	{"Core Services (Networking, Compute, Block Volume)", policyRef + "corepolicyreference_topic-ResourceTypes.htm"},
	{"Object Storage", policyRef + "objectstoragepolicyreference.htm"},
	{"Vault, Key Management and Secrets", policyRef + "keypolicyreference.htm"},
	{"Autonomous Database", "https://docs.oracle.com/en-us/iaas/autonomous-database-serverless/doc/autonomous-database-iam-policies.html"},
	{"Base Database", "https://docs.oracle.com/en/cloud/paas/base-database/iam-policy-details/index.html"},
	{"Kubernetes Engine", policyRef + "contengpolicyreference.htm"},
	{"File Storage", policyRef + "filestoragepolicyreference.htm"},
	{"Functions", policyRef + "functionspolicyreference.htm"},
	{"IAM", policyRef + "iampolicyreference.htm"},
	{"Load Balancing", policyRef + "lbpolicyreference.htm"},
	{"Logging", policyRef + "loggingpolicyreference.htm"},
	{"Container Registry", policyRef + "registrypolicyreference.htm"},
	{"Notifications", policyRef + "notificationpolicyreference.htm"},
	{"Streaming", policyRef + "streamingpolicyreference.htm"},
	{"DNS", policyRef + "dnspolicyreference.htm"},
	{"Monitoring", policyRef + "monitoringpolicyreference.htm"},
	{"Events", policyRef + "cloudeventspolicyreference.htm"},
	{"API Gateway", policyRef + "apigatewaypolicyreference.htm"},
	{"Certificates", policyRef + "certificatespolicyreference.htm"},
	{"Audit", policyRef + "auditpolicyreference.htm"},
	{"Resource Manager", policyRef + "resourcemanagerpolicyreference.htm"},
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

func get(url string) (string, error) {
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
		return string(body), nil
	}
	return "", lastErr
}

var (
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	spaceRe = regexp.MustCompile(`\s+`)
	rowRe   = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	cellRe  = regexp.MustCompile(`(?s)<t[hd][^>]*>(.*?)</t[hd]>`)
	codeRe  = regexp.MustCompile(`(?s)<code[^>]*>\s*([^<]+?)\s*</code>`)
	// headingRe matches section headings (h1-h6 and DITA section titles).
	headingRe = regexp.MustCompile(`(?is)<(h[1-6])[^>]*>(.*?)</h[1-6]>`)
	typeRe    = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	// Aggregates are named "<x>-family", or documented as such: "a policy
	// that uses <verb> dns is equivalent to …", "drgs (covers drg-object, …)".
	verbOfRe = regexp.MustCompile(`<code[^>]*>\s*&lt;verb&gt;\s*([a-z][a-z0-9-]*)\s*</code>`)
	coversRe = regexp.MustCompile(`<code[^>]*>\s*([a-z][a-z0-9-]*)\s*</code>\s*\(covers`)
)

func text(s string) string {
	return strings.TrimSpace(spaceRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(s, " ")), " "))
}

// verbs reads the "General Access Covered by Each Verb" table.
func verbs() ([]Verb, error) {
	body, err := get(verbsURL)
	if err != nil {
		return nil, err
	}
	var out []Verb
	for _, row := range rowRe.FindAllStringSubmatch(body, -1) {
		cells := cellRe.FindAllStringSubmatch(row[1], -1)
		if len(cells) != 3 {
			continue
		}
		name := text(cells[0][1])
		if !slices.Contains([]string{"inspect", "read", "use", "manage"}, name) {
			continue
		}
		out = append(out, Verb{Name: name, Target: text(cells[1][1]), Description: text(cells[2][1])})
	}
	if len(out) != 4 {
		return nil, fmt.Errorf("%s: found %d verbs, want 4", verbsURL, len(out))
	}
	return out, nil
}

// section returns the part of a page from its "Resource-Types" heading to
// the next heading that starts another topic (variables, verb details,
// permissions, examples).
func section(body string) string {
	hs := headingRe.FindAllStringSubmatchIndex(body, -1)
	start := -1
	for _, h := range hs {
		t := strings.ToLower(text(body[h[4]:h[5]]))
		if start < 0 {
			if strings.Contains(t, "resource-type") || strings.Contains(t, "resource type") {
				start = h[0]
			}
			continue
		}
		for _, stop := range []string{"variable", "details for verb", "permissions required", "example", "verb + resource"} {
			if strings.Contains(t, stop) {
				return body[start:h[0]]
			}
		}
	}
	if start < 0 {
		return ""
	}
	return body[start:]
}

func resourceTypes(p page) ([]ResourceType, error) {
	body, err := get(p.url)
	if err != nil {
		return nil, err
	}
	sec := section(body)
	if sec == "" {
		return nil, fmt.Errorf("%s: no Resource-Types section", p.url)
	}
	aggregate := map[string]bool{}
	for _, re := range []*regexp.Regexp{verbOfRe, coversRe} {
		for _, m := range re.FindAllStringSubmatch(sec, -1) {
			aggregate[m[1]] = true
		}
	}
	var out []ResourceType
	seen := map[string]bool{}
	for _, m := range codeRe.FindAllStringSubmatch(sec, -1) {
		name := strings.TrimSpace(html.UnescapeString(m[1]))
		if !typeRe.MatchString(name) || seen[name] || name == "all-resources" {
			continue
		}
		seen[name] = true
		out = append(out, ResourceType{Name: name, Service: p.service, Family: strings.HasSuffix(name, "-family") || aggregate[name], URL: p.url})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no resource-types", p.url)
	}
	return out, nil
}

func run(out string) error {
	vs, err := verbs()
	if err != nil {
		return err
	}
	f := File{
		Source: Source{Title: "OCI IAM Detailed Service Policy Reference", URL: referenceURL, Retrieved: time.Now().UTC().Format("2006-01-02")},
		Verbs:  vs,
		ResourceTypes: []ResourceType{{
			Name: "all-resources", Service: "All services", Family: true, URL: resourcesURL,
		}},
	}
	seen := map[string]bool{"all-resources": true}
	for _, p := range pages {
		rts, err := resourceTypes(p)
		if err != nil {
			return err
		}
		for _, rt := range rts {
			if !seen[rt.Name] {
				seen[rt.Name] = true
				f.ResourceTypes = append(f.ResourceTypes, rt)
			}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", " ")
	if err := enc.Encode(f); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	path := filepath.Join(out, "oci.json")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	fams := 0
	for _, rt := range f.ResourceTypes {
		if rt.Family {
			fams++
		}
	}
	log.Printf("wrote %s: %d verbs, %d resource-types (%d families, %d bytes)", path, len(f.Verbs), len(f.ResourceTypes), fams, buf.Len())
	return nil
}
