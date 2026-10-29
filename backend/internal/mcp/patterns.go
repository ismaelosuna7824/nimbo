package mcp

import (
	"context"
	"net/url"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/patterns"
)

// architecture_patterns: GET /patterns?requirement=&platform= and
// GET /patterns/{id} (registered with the Well-Architected tools).

type architecturePatternsIn struct {
	Requirement string `json:"requirement,omitempty" jsonschema:"the requirement in plain language (English or Spanish), e.g. \"orders API with spiky traffic that notifies billing and shipping\""`
	Platform    string `json:"platform,omitempty" jsonschema:"aws, azure, gcp, oci, docker or kubernetes: keeps only that platform's block; omit for any"`
	Pattern     string `json:"pattern,omitempty" jsonschema:"a pattern ID (e.g. cqrs, saga, queue-based-load-leveling) to get it in full"`
}

// patternListResp is the API's GET /patterns (api.patternList).
type patternListResp struct {
	Matches []patterns.Pattern `json:"matches"`
	Catalog []struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Kind      string   `json:"kind"`
		Problem   string   `json:"problem"`
		Platforms []string `json:"platforms"`
	} `json:"catalog"`
}

// patternMatchView is a ranked match: enough to choose, get pattern for the rest.
type patternMatchView struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Kind      string           `json:"kind"`
	Problem   string           `json:"problem"`
	UseWhen   []string         `json:"useWhen"`
	AvoidWhen []string         `json:"avoidWhen"`
	Blocks    []patterns.Block `json:"blocks,omitempty"`
}

type patternMatchesView struct {
	Matches []patternMatchView `json:"matches,omitempty"`
	Catalog any                `json:"catalog,omitempty"`
	Next    string             `json:"next"`
}

type patternView struct {
	patterns.Pattern
	Next string `json:"next"`
}

const patternsNext = "Pick the simplest style and the fewest patterns that meet the requirement; check each one's avoidWhen against it. Tell the user the choice with a one-line justification each, the trade-offs and what you deliberately did not add. architecture_patterns with pattern=<id> gives practices, trade-offs and the full platform blocks; then well_architected_guidance for the platform."

func (t *tools) architecturePatterns(ctx context.Context, in architecturePatternsIn) (any, error) {
	platform := strings.ToLower(strings.TrimSpace(in.Platform))
	if a, ok := platformAliases[platform]; ok {
		platform = a
	}
	if id := strings.TrimSpace(in.Pattern); id != "" {
		var p patterns.Pattern
		if err := t.c.get(ctx, "/patterns/"+pathEscape(strings.ToLower(id)), &p); err != nil {
			return nil, err
		}
		if platform != "" {
			p.Blocks = blocksFor(p.Blocks, platform)
		}
		return patternView{Pattern: p, Next: "Apply its practices as you build; the block types are the resources/services/kinds to add on the platform (search_resources, list_docker_images, list_kubernetes_catalog); related patterns often come with it, templates start from a worked example."}, nil
	}
	q := url.Values{}
	if r := strings.TrimSpace(in.Requirement); r != "" {
		q.Set("requirement", r)
	}
	if platform != "" {
		q.Set("platform", platform)
	}
	path := "/patterns"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var resp patternListResp
	if err := t.c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	if q.Get("requirement") == "" {
		return patternMatchesView{Catalog: resp.Catalog, Next: "Call again with requirement to rank them for the user's need, or with pattern=<id> for one in full."}, nil
	}
	out := patternMatchesView{Matches: make([]patternMatchView, 0, len(resp.Matches)), Next: patternsNext}
	for _, p := range resp.Matches {
		v := patternMatchView{ID: p.ID, Name: p.Name, Kind: p.Kind, Problem: p.Problem, UseWhen: p.UseWhen, AvoidWhen: p.AvoidWhen}
		if platform != "" {
			v.Blocks = blocksFor(p.Blocks, platform)
		}
		out.Matches = append(out.Matches, v)
	}
	if len(out.Matches) == 0 {
		out.Next = "No pattern matches: the requirement probably fits a plain n-tier or serverless design (architecture_patterns pattern=n-tier or serverless); keep it simple."
	}
	return out, nil
}

// blocksFor keeps the blocks of one platform.
func blocksFor(blocks []patterns.Block, platform string) []patterns.Block {
	var keep []patterns.Block
	for _, b := range blocks {
		if b.Platform == platform {
			keep = append(keep, b)
		}
	}
	return keep
}
