// Command gcp regenerates internal/access/data/gcp_roles.json.gz, the
// pinned list of Google Cloud IAM basic and predefined roles the GCP
// access editor and grants pick from, from the official IAM roles and
// permissions index (https://cloud.google.com/iam/docs/roles-permissions):
// the index's role list (its filter JSON: role ID, title, service page),
// each service page's role descriptions, and the basic roles of the roles
// overview page. The retrieval date is recorded in the file.
//
//	cd backend/internal/access && go run ./gen/gcp
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	site      = "https://docs.cloud.google.com"
	indexURL  = "https://cloud.google.com/iam/docs/roles-permissions"
	filterURL = site + "/iam/json/role-permission-filter.json"
	overview  = "/iam/docs/roles-overview"
	pageTitle = "IAM roles and permissions index (Google Cloud)"
	basicCat  = "Basic roles"
)

type source struct {
	Title     string `json:"title"`
	URL       string `json:"url"`
	Retrieved string `json:"retrieved"`
}

type role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Privileged  bool   `json:"privileged,omitempty"`
}

type filter struct {
	Services []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"services"`
	Roles []struct {
		Title string `json:"title"`
		Name  string `json:"name"`
		URL   string `json:"url"`
	} `json:"roles"`
}

var (
	roleRe = regexp.MustCompile(`(?s)<h4 class="role-title[^"]*" id="[^"]*"[^>]*>.*?</h4>\s*<p class="iamperm-marginless">\(<code[^>]*>roles/(?:<wbr>)?([^<]+)</code>\)</p>\s*(?:<span class="role-description[^"]*">(.*?)</span>)?`)
	// The roles overview's basic role rows (the description cell up to its
	// "For a list of permissions" console link).
	basicRe = regexp.MustCompile(`(?s)<td><strong>[^<]+</strong> \(<code[^>]*>(roles/[a-z]+)</code>\)</td>\s*<td>(.*?)</td>`)
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	wsRe    = regexp.MustCompile(`\s+`)
)

func main() {
	out := flag.String("out", "data/gcp_roles.json.gz", "output file (gzip)")
	flag.Parse()

	data, err := get(filterURL)
	if err != nil {
		log.Fatal(err)
	}
	var f filter
	if err := json.Unmarshal(data, &f); err != nil {
		log.Fatalf("%s: %v", filterURL, err)
	}
	if len(f.Roles) < 1000 {
		log.Fatalf("only %d roles in %s: the format changed", len(f.Roles), filterURL)
	}
	titles := map[string]string{} // page path -> service title
	for _, s := range f.Services {
		titles[s.URL] = s.Title
	}
	pages := map[string]bool{}
	for _, r := range f.Roles {
		pages[pagePath(r.URL)] = true
	}
	descs := fetchDescriptions(slices.Sorted(func(yield func(string) bool) {
		for p := range pages {
			if !yield(p) {
				return
			}
		}
	}))

	roles := make([]role, 0, len(f.Roles))
	seen := map[string]bool{}
	missing := 0
	for _, r := range f.Roles {
		if seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		p := pagePath(r.URL)
		x := role{ID: r.Name, Name: strings.TrimSpace(r.Title), Description: descs[r.Name], Category: titles[p]}
		if p == overview {
			x.Category = basicCat
			x.Privileged = true
		}
		if x.Description == "" {
			missing++
		}
		roles = append(roles, x)
	}
	slices.SortFunc(roles, func(a, b role) int { return strings.Compare(a.ID, b.ID) })

	var sb strings.Builder
	src, _ := json.Marshal(source{Title: pageTitle, URL: indexURL, Retrieved: time.Now().Format("2006-01-02")})
	fmt.Fprintf(&sb, "{\"source\":%s,\n\"origin\":%q,\n\"roles\":[\n", src, filterURL)
	for i, r := range roles {
		line, _ := json.Marshal(r)
		sb.Write(line)
		if i < len(roles)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("]}\n")
	var gz bytes.Buffer
	w, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	w.Write([]byte(sb.String()))
	w.Close()
	if err := os.WriteFile(*out, gz.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d roles (%d without description) from %d pages to %s", len(roles), missing, len(pages), *out)
}

// pagePath is the page of a role URL ("/iam/docs/roles-permissions/storage").
func pagePath(u string) string {
	p, _, _ := strings.Cut(u, "#")
	return p
}

// fetchDescriptions reads every page's role descriptions (role ID -> text).
func fetchDescriptions(pages []string) map[string]string {
	out := map[string]string{}
	var mu sync.Mutex
	work := make(chan string)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for p := range work {
				body, err := getRetry(site + p)
				if err != nil {
					log.Printf("%s: %v", p, err)
					continue
				}
				found := parsePage(p, string(body))
				mu.Lock()
				for k, v := range found {
					out[k] = v
				}
				mu.Unlock()
			}
		})
	}
	for _, p := range pages {
		work <- p
	}
	close(work)
	wg.Wait()
	return out
}

func parsePage(path, body string) map[string]string {
	out := map[string]string{}
	if path == overview {
		for _, m := range basicRe.FindAllStringSubmatch(body, -1) {
			desc, _, _ := strings.Cut(m[2], "For a list of permissions")
			out[m[1]] = clean(desc)
		}
		return out
	}
	for _, m := range roleRe.FindAllStringSubmatch(body, -1) {
		out["roles/"+strings.ReplaceAll(m[1], "<wbr>", "")] = clean(m[2])
	}
	return out
}

func clean(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	// Inline code and links leave a space before punctuation.
	for _, p := range []string{" .", " ,", " )", " :", " ;"} {
		s = strings.ReplaceAll(s, p, p[1:])
	}
	return strings.ReplaceAll(s, "( ", "(")
}

func getRetry(url string) ([]byte, error) {
	var err error
	for attempt := range 4 {
		var b []byte
		if b, err = get(url); err == nil {
			return b, nil
		}
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return nil, err
}

func get(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nimbo-access-gen")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
