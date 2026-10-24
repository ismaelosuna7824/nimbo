package mcp

import (
	"context"
	"errors"
	"net/url"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Learn: roadmaps and challenges (GET /learn…).

const groupLearn = "Learn"

func registerLearnTools(r *registry, t *tools) {
	r.group = groupLearn
	addTool(r, "list_learning_paths", "List learning paths",
		"Nimbo Learn roadmaps (beginner → intermediate → advanced → expert) built from official documentation. Without provider: every provider (aws, azure, gcp, oci, docker, kubernetes, global) with challenge counts per level, the user's completions and content load errors. With provider: its roadmap — per level the goals and steps, each step with its practice challenges (id, title, the user's status) and official docs.",
		t.listLearningPaths)
	addTool(r, "list_challenges", "List challenges",
		"List Learn challenges (hands-on problems Nimbo grades for real: validate, rules, structure, deploy, HTTP, load tests, incident scenarios, readiness) with the user's status. Filters: provider, level, tag, query (words in title/summary/problem/tags). Runtime is local (Docker/k3s), emulated (Floci, AWS) or design (validate-level only).",
		t.listChallenges)
	addTool(r, "get_challenge", "Get challenge",
		"Get one Learn challenge: problem, requirements, hints, the graded checks, starter, per-provider adaptations (global challenges), official sources, prerequisites and the user's progress (attempts, passed checks, workflow). The solution is left out unless include_solution is true: when coaching a learner, give hints first.",
		t.getChallenge)
}

type listLearningPathsIn struct {
	Provider string `json:"provider,omitempty" jsonschema:"aws, azure, gcp, oci, docker, kubernetes or global; omit to list the providers"`
}

type learnStepView struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Challenges  []learnStepChalView `json:"challenges,omitempty"`
	Docs        []string            `json:"docs"`
}

type learnStepChalView struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Level  string `json:"level,omitempty"`
	Status string `json:"status"`
}

type learnLevelView struct {
	Level string          `json:"level"`
	Title string          `json:"title"`
	Goals string          `json:"goals"`
	Steps []learnStepView `json:"steps"`
}

func (t *tools) listLearningPaths(ctx context.Context, in listLearningPathsIn) (any, error) {
	if in.Provider == "" {
		var out model.LearnOverview
		err := t.c.get(ctx, "/learn", &out)
		return out, err
	}
	var rm model.Roadmap
	if err := t.c.get(ctx, "/learn/providers/"+pathEscape(in.Provider)+"/roadmap", &rm); err != nil {
		return nil, err
	}
	// Titles and status of every linked challenge (global roadmaps link
	// other providers' challenges).
	var all []model.ChallengeSummary
	if err := t.c.get(ctx, "/learn/challenges", &all); err != nil {
		return nil, err
	}
	byID := map[string]model.ChallengeSummary{}
	for _, c := range all {
		byID[c.ID] = c
	}
	levels := make([]learnLevelView, 0, len(rm.Levels))
	for _, lvl := range rm.Levels {
		lv := learnLevelView{Level: lvl.Level, Title: lvl.Title, Goals: lvl.Goals}
		for _, st := range lvl.Steps {
			sv := learnStepView{Title: st.Title, Description: st.Description, Docs: []string{}}
			for _, id := range st.Challenges {
				c := byID[id]
				sv.Challenges = append(sv.Challenges, learnStepChalView{ID: id, Title: c.Title, Level: c.Level, Status: c.Progress.Status})
			}
			for _, d := range st.Docs {
				sv.Docs = append(sv.Docs, d.Title+" — "+d.URL)
			}
			lv.Steps = append(lv.Steps, sv)
		}
		levels = append(levels, lv)
	}
	return map[string]any{"id": rm.ID, "provider": rm.Provider, "title": rm.Title, "summary": rm.Summary, "levels": levels}, nil
}

type listChallengesIn struct {
	Provider string `json:"provider,omitempty" jsonschema:"aws, azure, gcp, oci, docker, kubernetes or global"`
	Level    string `json:"level,omitempty" jsonschema:"beginner, intermediate, advanced or expert"`
	Tag      string `json:"tag,omitempty"`
	Query    string `json:"query,omitempty" jsonschema:"words that must all appear in the title, summary, problem or tags"`
}

type challengeListView struct {
	ID       string   `json:"id"`
	Provider string   `json:"provider"`
	Level    string   `json:"level"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Minutes  int      `json:"minutes"`
	Runtime  string   `json:"runtime"`
	Tags     []string `json:"tags,omitempty"`
	Status   string   `json:"status"`
	// SolvableWithoutCode: every step can be done in the canvas and
	// inspector (false: some step needs code or file edits).
	SolvableWithoutCode *bool `json:"solvableWithoutCode,omitempty"`
}

func (t *tools) listChallenges(ctx context.Context, in listChallengesIn) (any, error) {
	q := url.Values{}
	for k, v := range map[string]string{"provider": in.Provider, "level": in.Level, "tag": in.Tag, "q": in.Query} {
		if v != "" {
			q.Set(k, v)
		}
	}
	path := "/learn/challenges"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var list []model.ChallengeSummary
	if err := t.c.get(ctx, path, &list); err != nil {
		return nil, err
	}
	out := make([]challengeListView, 0, len(list))
	for _, c := range list {
		out = append(out, challengeListView{ID: c.ID, Provider: c.Provider, Level: c.Level, Title: c.Title, Summary: c.Summary,
			Minutes: c.Minutes, Runtime: c.Runtime, Tags: c.Tags, Status: c.Progress.Status, SolvableWithoutCode: c.SolvableWithoutCode})
	}
	return out, nil
}

type getChallengeIn struct {
	ID              string `json:"id" jsonschema:"challenge ID from list_challenges"`
	IncludeSolution bool   `json:"include_solution,omitempty" jsonschema:"also return the reference solution and solution files"`
}

func (t *tools) getChallenge(ctx context.Context, in getChallengeIn) (any, error) {
	if in.ID == "" {
		return nil, errors.New("id is required (see list_challenges)")
	}
	var d model.ChallengeDetail
	if err := t.c.get(ctx, "/learn/challenges/"+pathEscape(in.ID), &d); err != nil {
		return nil, err
	}
	if !in.IncludeSolution {
		d.Challenge.Solution = ""
		d.Challenge.SolutionFiles = nil
	}
	return d, nil
}
