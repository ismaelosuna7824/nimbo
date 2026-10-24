// Command azure regenerates internal/access/data/azure_roles.json, the
// pinned list of Azure built-in roles the Azure access editor and grants
// pick from, from the official "Azure built-in roles" page
// (https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles).
// It reads the page's Markdown source in MicrosoftDocs/azure-docs at the
// latest commit that changed it and records that commit and the retrieval
// date in the file.
//
//	cd backend/internal/access && go run ./gen/azure
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	pageURL   = "https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles"
	repo      = "MicrosoftDocs/azure-docs"
	docPath   = "articles/role-based-access-control/built-in-roles.md"
	pageTitle = "Azure built-in roles (Microsoft Learn)"
	// privilegedCategory is the page section of the roles Microsoft calls
	// privileged administrator roles (Owner, Contributor, ...).
	privilegedCategory = "Privileged"
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

var (
	// > | <a name='acrpull'></a>[AcrPull](./built-in-roles/containers.md#acrpull) | Pull artifacts ... | 7f951dda-... |
	rowRe  = regexp.MustCompile(`^>\s*\|\s*(?:<a name='[^']*'></a>)?\[(.+?)\]\([^)]*\)\s*\|\s*(.*?)\s*\|\s*([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\s*\|?\s*$`)
	linkRe = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	tagRe  = regexp.MustCompile(`<[^>]+>`)
	wsRe   = regexp.MustCompile(`\s+`)
)

func main() {
	out := flag.String("out", "data/azure_roles.json", "output file")
	flag.Parse()

	sha, err := latestCommit()
	if err != nil {
		log.Fatal(err)
	}
	raw := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, sha, docPath)
	md, err := get(raw)
	if err != nil {
		log.Fatal(err)
	}
	roles, skipped := parse(string(md))
	if len(roles) < 300 {
		log.Fatalf("only %d roles parsed from %s: the page format changed", len(roles), raw)
	}
	for _, s := range skipped {
		log.Printf("skipped row: %s", s)
	}

	var sb strings.Builder
	src, _ := json.Marshal(source{Title: pageTitle, URL: pageURL, Retrieved: time.Now().Format("2006-01-02")})
	fmt.Fprintf(&sb, "{\"source\":%s,\n\"origin\":%q,\n\"roles\":[\n", src, raw)
	for i, r := range roles {
		line, _ := json.Marshal(r)
		sb.Write(line)
		if i < len(roles)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("]}\n")
	if err := os.WriteFile(*out, []byte(sb.String()), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d roles to %s (commit %s)", len(roles), *out, sha)
}

// parse reads the summary tables: one "## Category" section per table.
func parse(md string) (roles []role, skipped []string) {
	category := ""
	seen := map[string]bool{}
	for line := range strings.SplitSeq(md, "\n") {
		line = strings.TrimRight(line, "\r")
		if h, ok := strings.CutPrefix(line, "## "); ok {
			category = strings.TrimSpace(h)
			continue
		}
		if !strings.HasPrefix(line, "> | ") || strings.HasPrefix(line, "> | ---") || strings.HasPrefix(line, "> | Built-in role") {
			continue
		}
		m := rowRe.FindStringSubmatch(line)
		if m == nil {
			skipped = append(skipped, line)
			continue
		}
		id := strings.ToLower(m[3])
		if seen[id] {
			continue
		}
		seen[id] = true
		roles = append(roles, role{
			ID:          id,
			Name:        clean(m[1]),
			Description: clean(m[2]),
			Category:    category,
			Privileged:  category == privilegedCategory,
		})
	}
	return roles, skipped
}

func clean(s string) string {
	s = linkRe.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "<br/>", " ")
	s = strings.ReplaceAll(s, "<br>", " ")
	s = tagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, `\|`, "|")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

func latestCommit() (string, error) {
	data, err := get(fmt.Sprintf("https://api.github.com/repos/%s/commits?path=%s&per_page=1", repo, docPath))
	if err != nil {
		return "", err
	}
	var commits []struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(data, &commits); err != nil || len(commits) == 0 {
		return "", fmt.Errorf("no commit for %s: %v", docPath, err)
	}
	return commits[0].SHA, nil
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
