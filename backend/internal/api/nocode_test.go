package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ---------------------------------------------------------------------------
// No-code audit: a challenge that declares solvableWithoutCode: true must be
// solvable with the canvas and inspectors alone. Its script
// (testdata/nocode/<challenge id>.yaml) replays the solution as UI
// gestures, each sent as the exact request the desktop app sends, on a
// challenge started the way the app starts it; the static checks must then
// all pass. No step edits files, Dockerfiles, application code or writes a
// raw expression: that is what "requires code" means.
//
//	cd backend && go test ./internal/api/ -run TestNoCode
//	cd backend && go test ./internal/api/ -run TestNoCode/aws-vpc-multi-az-subnets -v
//
// Script: `steps:` is a list of one-key maps, the key naming the gesture:
//
// Terraform (canvas, inspector, variables panel, access editors)
//   add {type, kind?: resource|data, name?}  palette drop (POST nodes) + Terraform name (PATCH name)
//   rename {node, name}                      inspector Terraform name (PATCH name)
//   delete {node}                            delete (DELETE nodes)
//   set {node, attrs: {path: value}}         inspector form edits, saved like the store (GET + PUT workflow).
//                                            path: attr, block[i].attr, block[i].sub[j].attr (i = count adds
//                                            a block, like "Add"), block[i] with {unset: true} removes it;
//                                            pro-mode meta-arguments: count, depends_on [addresses],
//                                            provider (an alias), lifecycle.create_before_destroy/prevent_destroy.
//                                            value: a literal for the attribute's widget (string, number,
//                                            bool, list of strings, map of strings), {unset: true},
//                                            {link: X} (Link to…: X is another node's attribute the picker
//                                            lists — top level, or behind "More…" block[0].attr, list(object)
//                                            element attr[0].field, list element attr[0..2]; count nodes as
//                                            addr[0..2].attr, for_each nodes as addr["key"].attr — or
//                                            var.<name> / local.<name>, of a type that fits the field; quote
//                                            paths with [i] in YAML flow maps), {link: X, append: true} (Link
//                                            to… on a list of strings appends a single value), {link: X,
//                                            base64: true} (Base64-encode on user_data fields), {remove: X}
//                                            (the X of a chip of a list holding references); for list(object)
//                                            attributes the object-list editor's cards: [{field: value}, …]
//                                            (every card, written as blocks; a card's list field may mix
//                                            texts and {link: X} items); for map(string) attributes the map
//                                            editor's final rows {key: text | {link: X} | {keep: true}}
//                                            ({keep: true}: a row holding an expression left untouched;
//                                            {link: X, base64: true} on a user_data row).
//   variable {name, type?, default?, removeDefault?, description?, sensitive?}   Variables panel (GET + PUT workflow)
//   provider {name, alias?, region?, remove?}   Providers section of the workflow overview (GET + PUT
//                                            workflow): region a text or {link: var.<name>}; remove refused
//                                            while resources use the configuration
//   connect {source, target, recipe}         drag an edge and pick a listed recipe (GET + POST connections;
//                                            grants: recipe access:<level>)
//   accessAWS {node, path, removeWhere?, add?, update?}   AWS policy editor (GET + PUT access)
//   accessAzure {node, set} / accessGCP {node, set} / accessOCI {node, remove?, add?}
// Docker (canvas, inspector, environments)
//   composeAddService {catalogId} | {build: {starter?}}   palette (POST compose/services)
//   composePatch {service, patch}            service form incl. Hardening and Logging (PATCH compose/services/{name};
//                                            form keys and values the widgets produce; scope: environment is the
//                                            "Only in <env>" switch)
//   composeEnv {service, set?, remove?}      the form's Environment list (sent whole, like the form)
//   composeStage {node, from}                "Base image (FROM)" of a stage card or a single-stage build's
//                                            service (PATCH compose/stages/{node}: rewrites that FROM line)
//   composeDeleteService {service}
//   composeAddTop {kind: network|volume, name?}   palette (POST, then rename like the inspector)
//   composePatchTop {kind, name, patch} / composeDeleteTop {kind, name}
//   composeConnect {from, to, recipe, target?}    drag an edge and pick a listed recipe
//   envCreate {name, from?} / envSettings {env, target?, values} / envVariable {env, key, value?, secret?, remove?}
//   envActivate {env}                        the environment switch (PUT workflow activeEnvironment)
//   envDelete {env}                          Delete in an environment's menu (nocode_kube_test.go)
// Kubernetes (canvas, inspector; extra form paths in nocode_kube_test.go)
//   kubeAdd {itemId}                         palette (POST kubernetes/catalog)
//   kubePatch {node, set?, unset?}           inspector forms (PATCH kubernetes/objects; form paths only)
//   kubeRename {node, name}                  the inspector header's name (plain manifests: metadata.name)
//   kubeConnect {from, to, recipe, port?, path?, host?, key?}   drag an edge (+ the recipe's inputs);
//                                            to "" = a recipe of the object's context menu
//   kubeAccess {node, rules?, roleRef?, subjects?, serviceAccount?, automountToken?}   RBAC editor
//   kubeDelete {node}

const nocodeDir = "testdata/nocode"

// nocodeStaticTypes are the checks graded without a runtime (as in the
// challenge content tests); the others are reported as not run.
var nocodeStaticTypes = []string{model.CheckValidate, model.CheckRulesClean, model.CheckHas}

type nocodeScript struct {
	Steps []yaml.Node `yaml:"steps"`
}

func TestNoCodeChallenges(t *testing.T) {
	e := newEnv(t)
	base := e.srv.URL + "/api/v1"

	var summaries []model.ChallengeSummary
	if err := nocodeRequest(base, "GET", "/learn/challenges", nil, &summaries); err != nil {
		t.Fatal(err)
	}
	known := map[string]model.ChallengeSummary{}
	for _, c := range summaries {
		known[c.ID] = c
	}
	scripts := map[string]string{}
	entries, err := os.ReadDir(nocodeDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		id, ok := strings.CutSuffix(en.Name(), ".yaml")
		if !ok || en.IsDir() {
			t.Errorf("%s/%s: only <challenge id>.yaml scripts belong here", nocodeDir, en.Name())
			continue
		}
		if _, found := known[id]; !found {
			t.Errorf("%s/%s: no challenge %s in the content", nocodeDir, en.Name(), id)
			continue
		}
		scripts[id] = filepath.Join(nocodeDir, en.Name())
	}
	// A challenge declared solvable without code has a script; one that
	// requires code has none.
	for _, id := range slices.Sorted(maps.Keys(known)) {
		_, has := scripts[id]
		switch v := known[id].SolvableWithoutCode; {
		case v == nil:
		case *v && !has:
			t.Errorf("challenge %s declares solvableWithoutCode: true but has no %s/%s.yaml", id, nocodeDir, id)
		case !*v && has:
			t.Errorf("challenge %s declares solvableWithoutCode: false but has a no-code script %s", id, scripts[id])
		}
	}
	for _, id := range slices.Sorted(maps.Keys(scripts)) {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			runNocodeScript(t, e, base, id, scripts[id])
		})
	}
}

// runNocodeScript starts the challenge, replays the script and grades the
// static checks of the resulting workflow.
func runNocodeScript(t *testing.T, e *testEnv, base, id, file string) {
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var script nocodeScript
	if err := yaml.Load(data, &script, yaml.WithKnownFields()); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if len(script.Steps) == 0 {
		t.Fatalf("%s: no steps", file)
	}
	var wf model.Workflow
	if err := nocodeRequest(base, "POST", "/learn/challenges/"+id+"/start", map[string]string{}, &wf); err != nil {
		t.Fatalf("start: %v", err)
	}
	r := &nocodeRun{base: base, wfID: wf.ID, schemas: map[string]*model.ResourceSchema{}}
	for i := range script.Steps {
		op, err := r.step(&script.Steps[i])
		if err != nil {
			t.Fatalf("%s: step %d (%s, line %d): %v", file, i+1, op, script.Steps[i].Line, err)
		}
	}

	var c model.ChallengeDetail
	if err := nocodeRequest(base, "GET", "/learn/challenges/"+id, nil, &c); err != nil {
		t.Fatal(err)
	}
	final, err := e.st.Get(wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	var static []model.ChallengeCheck
	for _, k := range c.Challenge.Checks {
		if slices.Contains(nocodeStaticTypes, k.Type) {
			static = append(static, k)
		} else {
			t.Logf("not run: %s (%s) needs the runtime", k.ID, k.Type)
		}
	}
	// A Terraform solution must be valid against the provider schemas,
	// whether or not the challenge grades validate (as for the reference
	// solutions).
	if final.IsTerraform() && !slices.ContainsFunc(static, func(k model.ChallengeCheck) bool { return k.Type == model.CheckValidate }) {
		static = append(static, model.ChallengeCheck{ID: "schema-valid", Label: "Valid against the provider schemas", Type: model.CheckValidate})
	}
	analyses := map[string]*challenge.Analysis{}
	for _, k := range static {
		a, ok := analyses[k.Environment]
		if !ok {
			if a, err = challenge.Analyze(context.Background(), testCatalog, final, e.st.AssetsDir(wf.ID), k.Environment); err != nil {
				t.Errorf("%s (%s): analyze: %v", k.ID, k.Type, err)
				continue
			}
			analyses[k.Environment] = a
		}
		res := challenge.GradeStatic(k, a)
		if !res.Passed {
			t.Errorf("fails %s (%s) %q: %s", k.ID, k.Type, k.Label, res.Detail)
		}
	}
}

// nocodeRun is one script replay on one challenge workflow.
type nocodeRun struct {
	base    string
	wfID    string
	schemas map[string]*model.ResourceSchema // kind/type → schema (GET /catalog/resources)
}

// step decodes and runs one gesture, returning its name.
func (r *nocodeRun) step(n *yaml.Node) (string, error) {
	if n.Kind != yaml.MappingNode || len(n.Content) != 2 {
		return "?", fmt.Errorf("a step is a map with one key, the gesture")
	}
	op, arg := n.Content[0].Value, n.Content[1]
	run, found := nocodeOps[op]
	if !found {
		return op, fmt.Errorf("unknown gesture %q (not a UI operation: %s)", op, strings.Join(slices.Sorted(maps.Keys(nocodeOps)), ", "))
	}
	return op, run(r, arg)
}

// decodeArg decodes a gesture's argument, rejecting unknown keys.
func decodeArg(n *yaml.Node, v any) error {
	return n.Load(v, yaml.WithKnownFields())
}

func (r *nocodeRun) call(method, path string, body, out any) error {
	return nocodeRequest(r.base, method, path, body, out)
}

func (r *nocodeRun) wfPath(rest string) string { return "/workflows/" + r.wfID + rest }

func (r *nocodeRun) workflow() (*model.Workflow, error) {
	var wf model.Workflow
	if err := r.call("GET", r.wfPath(""), nil, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

// nocodeRequest sends a JSON request and decodes a 2xx response into out.
func nocodeRequest(base, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s = %d: %s", method, path, res.StatusCode, bytes.TrimSpace(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
	}
	return nil
}
