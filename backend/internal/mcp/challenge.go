package mcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Challenge workspaces: start_challenge (POST /learn/challenges/{id}/start)
// and check_challenge (POST /workflows/{id}/challenge/check, then GET
// /workflows/{id}/challenge until the check ends).

func registerChallengeTools(r *registry, t *tools) {
	r.group = groupLearn
	addTool(r, "start_challenge", "Start challenge",
		"Create a workflow from a Learn challenge's starter (docker, Kubernetes or Terraform), linked to the challenge, and mark it in progress. "+
			"The starter is broken on purpose: the learner (or you, when asked to solve it) edits it until check_challenge passes. Returns the new workflow's id, name and kind.",
		t.startChallenge)
	addTool(r, "check_challenge", "Check challenge",
		"Grade a challenge workflow for real (\"Check my solution\"): every check in order — validate, rules_clean and has on the files (every best-practice rule on), then a deploy (compose up / k3s up / Floci apply) before the first runtime check, http calls, load tests and incident scenarios (load tests that inject a fault and revert it), and readiness. "+
			"Waits for the check to end (runtime checks take minutes) unless wait is false; returns per check its status, measured vs expected values, detail and load test run id (inspect with get_load_test), plus passed and a 0–100 score. Progress is recorded in Learn. "+
			"A failed check's detail says what to change; use get_challenge for hints. Only one check per workflow runs at a time.",
		t.checkChallenge)
}

type startChallengeIn struct {
	Challenge string `json:"challenge" jsonschema:"challenge id, e.g. docker-cache-aside (list_challenges)"`
	Name      string `json:"name,omitempty" jsonschema:"workflow name (default: the challenge title)"`
}

func (t *tools) startChallenge(ctx context.Context, in startChallengeIn) (any, error) {
	if in.Challenge == "" {
		return nil, fmt.Errorf("challenge is required (list_challenges)")
	}
	var wf model.Workflow
	if err := t.c.do(ctx, http.MethodPost, "/learn/challenges/"+pathEscape(in.Challenge)+"/start", map[string]string{"name": in.Name}, &wf); err != nil {
		return nil, err
	}
	return map[string]string{"workflow": wf.ID, "name": wf.Name, "kind": model.KindLabel(wf.Kind), "challenge": wf.Challenge,
		"hint": "edit the workflow, then call check_challenge"}, nil
}

type checkChallengeIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"challenge workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Wait     *bool  `json:"wait,omitempty" jsonschema:"wait for the check to end (default true, up to 25 minutes)"`
}

type challengeGoalView struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail,omitempty"`
	Measured []string `json:"measured,omitempty"`
	RunID    string   `json:"load_test_run,omitempty"`
}

type challengeResultView struct {
	Challenge string              `json:"challenge"`
	Status    string              `json:"status"`
	Passed    bool                `json:"passed"`
	Score     int                 `json:"score"`
	Phase     string              `json:"running,omitempty"`
	Error     string              `json:"error,omitempty"`
	Checks    []challengeGoalView `json:"checks"`
	Hint      string              `json:"hint,omitempty"`
}

func viewChallengeResult(r *model.ChallengeResult) challengeResultView {
	v := challengeResultView{Challenge: r.Challenge, Status: r.Status, Passed: r.Passed, Score: r.Score, Phase: r.Phase, Error: r.Error, Checks: []challengeGoalView{}}
	for _, g := range r.Goals {
		gv := challengeGoalView{ID: g.ID, Label: g.Label, Type: g.Type, Status: g.Status, Detail: g.Detail}
		if g.Type == model.CheckLoad || g.Type == model.CheckScenario {
			gv.RunID = g.RunID
		}
		for _, c := range g.Checks {
			mark := "✗"
			if c.Passed {
				mark = "✓"
			}
			gv.Measured = append(gv.Measured, fmt.Sprintf("%s %s: %s (needs %s)", mark, c.Label, c.Actual, c.Expected))
		}
		v.Checks = append(v.Checks, gv)
	}
	return v
}

func (t *tools) checkChallenge(ctx context.Context, in checkChallengeIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if wf.Challenge == "" {
		return nil, fmt.Errorf("%s is not a challenge workflow: start one with start_challenge", wf.Name)
	}
	path := "/workflows/" + pathEscape(wf.ID) + "/challenge"
	var res model.ChallengeResult
	if err := t.c.do(ctx, http.MethodPost, path+"/check", nil, &res); err != nil {
		return nil, err
	}
	if in.Wait != nil && !*in.Wait {
		v := viewChallengeResult(&res)
		v.Hint = "started; the app's Challenge tab shows live progress (a new check_challenge call while it runs is refused)"
		return v, nil
	}
	deadline := time.Now().Add(25 * time.Minute)
	for res.Status == model.ChallengeRunning && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		var view model.ChallengeView
		if err := t.c.get(ctx, path, &view); err != nil {
			return nil, err
		}
		if view.Last != nil {
			res = *view.Last
		}
	}
	v := viewChallengeResult(&res)
	if res.Status == model.ChallengeRunning {
		v.Hint = "still running; the app's Challenge tab shows live progress"
	}
	return v, nil
}
