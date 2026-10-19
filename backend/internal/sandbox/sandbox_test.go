package sandbox

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type fakeResolver struct {
	ports  map[int]int
	stages map[string][]string
}

func (f fakeResolver) hostPort(p int) int           { return f.ports[p] }
func (f fakeResolver) apiStages(id string) []string { return f.stages[id] }

func TestRewriteURL(t *testing.T) {
	r := fakeResolver{
		ports: map[int]int{4566: 50001, 4577: 50002, 4588: 50003, 4599: 50004},
		stages: map[string][]string{
			"httpapi": {"$default", "dev"},
			"restapi": {"prod"},
		},
	}
	cases := map[string]string{
		"http://localhost:4566/000000000000/q":                          "http://127.0.0.1:50001/000000000000/q",
		"https://localhost:4577/sub/rg?x=1":                             "http://127.0.0.1:50002/sub/rg?x=1",
		"http://127.0.0.1:4588/storage/v1/b":                            "http://127.0.0.1:50003/storage/v1/b",
		"http://localhost:4599/n/floci-local/b/x":                       "http://127.0.0.1:50004/n/floci-local/b/x",
		"http://bucket.s3.localhost.floci.io:4566/key":                  "http://bucket.s3.localhost.floci.io:50001/key",
		"http://acct.blob.localhost:4577/c":                             "http://acct.blob.localhost:50002/c",
		"https://httpapi.execute-api.us-east-1.amazonaws.com":           "http://127.0.0.1:50001/execute-api/httpapi/$default/",
		"https://httpapi.execute-api.us-east-1.amazonaws.com/hello":     "http://127.0.0.1:50001/execute-api/httpapi/$default/hello",
		"https://httpapi.execute-api.us-east-1.amazonaws.com/dev/hello": "http://127.0.0.1:50001/execute-api/httpapi/dev/hello",
		"http://restapi.execute-api.localhost.floci.io:4566/prod/a?b=1": "http://127.0.0.1:50001/execute-api/restapi/prod/a?b=1",
		"https://unknown.execute-api.us-east-1.amazonaws.com/v1/x":      "http://127.0.0.1:50001/execute-api/unknown/v1/x",
		"https://u1x2.lambda-url.us-east-1.on.aws/":                     "http://127.0.0.1:50001/lambda-url/u1x2/",
		"http://u1x2.lambda-url.localhost.floci.io:4566/p/q":            "http://127.0.0.1:50001/lambda-url/u1x2/p/q",
		"http://localhost:8080/x":                                       "http://localhost:8080/x",
		"https://example.com:4566/x":                                    "https://example.com:4566/x",
		"https://s3.amazonaws.com/bucket":                               "https://s3.amazonaws.com/bucket",
		"not a url":                                                     "not a url",
	}
	for in, want := range cases {
		if got := rewriteURL(in, r); got != want {
			t.Errorf("rewriteURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := rewriteURL("http://localhost:4566/x", fakeResolver{}); got != "http://localhost:4566/x" {
		t.Errorf("URL rewritten without a known host port: %q", got)
	}
}

func TestParseSummary(t *testing.T) {
	cases := map[string]*model.PlanSummary{
		"Plan: 3 to add, 1 to change, 2 to destroy.":                              {Add: 3, Change: 1, Destroy: 2},
		"Plan: 1 to import, 2 to add, 0 to change, 0 to destroy.":                 {Add: 2},
		"Apply complete! Resources: 5 added, 0 changed, 1 destroyed.":             {Add: 5, Destroy: 1},
		"Apply complete! Resources: 1 imported, 0 added, 0 changed, 0 destroyed.": {},
		"Destroy complete! Resources: 7 destroyed.":                               {Destroy: 7},
		"No changes. Your infrastructure matches the configuration.":              {},
		"aws_s3_bucket.b: Creating...":                                            nil,
	}
	for line, want := range cases {
		got, ok := parseSummary(line)
		if want == nil {
			if ok {
				t.Errorf("parseSummary(%q) matched: %+v", line, got)
			}
			continue
		}
		if !ok || got != *want {
			t.Errorf("parseSummary(%q) = %+v, %v; want %+v", line, got, ok, *want)
		}
	}
}

func TestSyncWorkdirKeepsStateAndReplacesConfig(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	assets := t.TempDir()
	mustWrite(t, filepath.Join(assets, "src", "index.mjs"), "v1")
	mustWrite(t, filepath.Join(assets, "src", "old.mjs"), "dropped later")
	mustWrite(t, filepath.Join(assets, "terraform.tfstate"), "assets state must not be copied")
	mustWrite(t, filepath.Join(assets, ".terraform", "x"), "no")

	// A workdir left by a hand-made setup plus Terraform's own artifacts.
	mustWrite(t, filepath.Join(work, "old.tf"), "stale")
	mustWrite(t, filepath.Join(work, "old.auto.tfvars"), "stale")
	mustWrite(t, filepath.Join(work, "terraform.tfstate"), "STATE")
	mustWrite(t, filepath.Join(work, "terraform.tfstate.backup"), "BACKUP")
	mustWrite(t, filepath.Join(work, ".terraform.lock.hcl"), "LOCK")
	mustWrite(t, filepath.Join(work, ".terraform", "providers", "p"), "PLUGIN")
	mustWrite(t, filepath.Join(work, "build", "fn.zip"), "ZIP")

	files := map[string][]byte{
		"main.tf":                      []byte("resource x y {}"),
		"modules/m/main.tf":            []byte("variable a {}"),
		"environments/dev.tfvars":      []byte("a = 1"),
		"environments/dev.env.example": []byte("K="),
	}
	resized, err := syncWorkdir(work, files, assets)
	if err != nil {
		t.Fatal(err)
	}
	if len(resized) != 0 {
		t.Errorf("first sync resized %v, want none (every file is new)", resized)
	}
	want := []string{
		".nimbo/manifest.json", ".terraform.lock.hcl", ".terraform/providers/p", "build/fn.zip",
		"main.tf", "modules/m/main.tf", "src/index.mjs", "src/old.mjs", "terraform.tfstate", "terraform.tfstate.backup",
	}
	if got := listTree(t, work); !slices.Equal(got, want) {
		t.Errorf("after first sync: %v\nwant %v", got, want)
	}

	// Second run: an asset and a module disappear, main.tf changes.
	if err := os.Remove(filepath.Join(assets, "src", "old.mjs")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(assets, "src", "index.mjs"), "v2")
	resized, err = syncWorkdir(work, map[string][]byte{"main.tf": []byte("changed")}, assets)
	if err != nil {
		t.Fatal(err)
	}
	// index.mjs keeps its size (v1 -> v2): only main.tf needs settling.
	if !maps.Equal(resized, map[string]int64{"main.tf": 7}) {
		t.Errorf("second sync resized %v, want main.tf: 7", resized)
	}
	want = []string{
		".nimbo/manifest.json", ".terraform.lock.hcl", ".terraform/providers/p", "build/fn.zip",
		"main.tf", "src/index.mjs", "terraform.tfstate", "terraform.tfstate.backup",
	}
	if got := listTree(t, work); !slices.Equal(got, want) {
		t.Errorf("after second sync: %v\nwant %v", got, want)
	}
	for p, content := range map[string]string{"terraform.tfstate": "STATE", "main.tf": "changed", "src/index.mjs": "v2"} {
		if b, _ := os.ReadFile(filepath.Join(work, p)); string(b) != content {
			t.Errorf("%s = %q, want %q", p, b, content)
		}
	}
	if _, err := syncWorkdir(work, map[string][]byte{"../escape.tf": nil}, ""); err == nil {
		t.Errorf("path escaping the workdir accepted")
	}
}

func TestParseStateMapsNodes(t *testing.T) {
	wf := &model.Workflow{ID: "wf", Nodes: []model.Node{
		{ID: "n-bucket", Kind: model.KindResource, Type: "aws_s3_bucket", Name: "b"},
		{ID: "n-zip", Kind: model.KindData, Type: "archive_file", Name: "zip"},
		{ID: "n-vpc", Kind: model.KindModule, Type: "module", Name: "vpc"},
	}}
	data := []byte(`{"values":{
	  "outputs":{"url":{"sensitive":false,"value":"http://x"},"pw":{"sensitive":true,"value":"p"}},
	  "root_module":{"resources":[
	    {"address":"aws_s3_bucket.b[0]","mode":"managed","type":"aws_s3_bucket","name":"b","index":0,"values":{"bucket":"b0"}},
	    {"address":"data.archive_file.zip","mode":"data","type":"archive_file","name":"zip","values":{}},
	    {"address":"aws_sqs_queue.unknown","mode":"managed","type":"aws_sqs_queue","name":"unknown","values":{}}
	  ],"child_modules":[{"address":"module.vpc[\"a\"]","resources":[
	    {"address":"module.vpc[\"a\"].aws_vpc.this","mode":"managed","type":"aws_vpc","name":"this","values":{}}
	  ],"child_modules":[{"address":"module.vpc[\"a\"].module.sub","resources":[
	    {"address":"module.vpc[\"a\"].module.sub.aws_subnet.s","mode":"managed","type":"aws_subnet","name":"s","values":{}}
	  ]}]}]}}}`)
	st, err := parseState(data, wf)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range st.Resources {
		got[r.Address] = r.NodeID
	}
	want := map[string]string{
		"aws_s3_bucket.b[0]":                      "n-bucket",
		"data.archive_file.zip":                   "n-zip",
		"aws_sqs_queue.unknown":                   "",
		`module.vpc["a"].aws_vpc.this`:            "n-vpc",
		`module.vpc["a"].module.sub.aws_subnet.s`: "n-vpc",
	}
	if len(got) != len(want) {
		t.Fatalf("resources = %v", got)
	}
	for addr, id := range want {
		if got[addr] != id {
			t.Errorf("%s -> %q, want %q", addr, got[addr], id)
		}
	}
	if st.Outputs["url"] != "http://x" || st.Outputs["pw"] != "(sensitive)" {
		t.Errorf("outputs = %v", st.Outputs)
	}
}

func TestParsePortOutput(t *testing.T) {
	got := parsePortOutput("4566/tcp -> 127.0.0.1:55001\n4577/tcp -> 127.0.0.1:55002\n4577/tcp -> [::1]:55009\n")
	if got[4566] != 55001 || got[4577] != 55002 || len(got) != 2 {
		t.Errorf("parsePortOutput = %v", got)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
