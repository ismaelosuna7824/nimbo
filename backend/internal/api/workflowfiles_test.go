package api

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// importDev imports the fixture's envs/dev project: generated Terraform with
// a tfvars environment, local modules, a Lambda source and a lock file.
func importDev(t *testing.T, e *testEnv) model.Workflow {
	t.Helper()
	return e.importOne(map[string]any{"dir": fixtureRepo(t), "projects": []string{"envs/dev"}})
}

func listWorkflowFiles(t *testing.T, e *testEnv, id string) map[string]model.WorkflowFile {
	t.Helper()
	var res struct {
		Files []model.WorkflowFile `json:"files"`
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+id+"/files", nil, &res)
	out := map[string]model.WorkflowFile{}
	for i, f := range res.Files {
		if i > 0 && res.Files[i-1].Path >= f.Path {
			t.Errorf("files not sorted: %s before %s", res.Files[i-1].Path, f.Path)
		}
		out[f.Path] = f
	}
	return out
}

func filePath(id, rel string) string {
	return "/workflows/" + id + "/files/content?path=" + url.QueryEscape(rel)
}

func TestWorkflowFilesListsEverythingExported(t *testing.T) {
	e := newEnv(t)
	wf := importDev(t, e)
	assets := e.st.AssetsDir(wf.ID)
	if err := os.WriteFile(filepath.Join(assets, "src", "layer.zip"), []byte("PK\x03\x04\x00\x00binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	files := listWorkflowFiles(t, e, wf.ID)
	want := map[string]model.WorkflowFile{
		"main.tf":                   {Kind: model.FileTerraform, Generated: true, Editable: true, Language: "hcl"},
		"terraform.tfvars":          {Kind: model.FileTfvars, Generated: true, Language: "hcl"},
		".terraform.lock.hcl":       {Kind: model.FileAsset, Language: "hcl"},
		"modules/network/main.tf":   {Kind: model.FileModule, Editable: true, Language: "hcl"},
		"modules/network/README.md": {Kind: model.FileModule, Editable: true, Language: "markdown"},
		"modules/subnets/main.tf":   {Kind: model.FileModule, Editable: true, Language: "hcl"},
		"src/handler.py":            {Kind: model.FileAsset, Editable: true, Language: "python"},
		"src/layer.zip":             {Kind: model.FileAsset, Binary: true, Language: "text"},
		"README.md":                 {Kind: model.FileScaffold, Generated: true, Language: "markdown"},
		".gitignore":                {Kind: model.FileScaffold, Generated: true, Language: "text"},
		"terraform.tfvars.example":  {Kind: model.FileScaffold, Generated: true, Language: "text"},
	}
	for path, w := range want {
		got, found := files[path]
		if !found {
			t.Errorf("%s not listed; files = %v", path, files)
			continue
		}
		w.Path, w.Size = path, got.Size
		if got != w {
			t.Errorf("%s = %+v, want %+v", path, got, w)
		}
		if got.Size <= 0 {
			t.Errorf("%s size = %d", path, got.Size)
		}
	}

	// The listing is what an export writes.
	dir := t.TempDir()
	var exported struct{ Files []string }
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": dir, "environments": true}, &exported)
	if len(exported.Files) != len(files) {
		t.Errorf("export wrote %v, listing has %d files", exported.Files, len(files))
	}
	for _, p := range exported.Files {
		if _, found := files[p]; !found {
			t.Errorf("exported %s is not listed", p)
		}
	}

	var main model.WorkflowFileContent
	e.mustCall(http.StatusOK, "GET", filePath(wf.ID, "main.tf"), nil, &main)
	if !main.Generated || !strings.Contains(main.Content, `module "network"`) || main.Size != int64(len(main.Content)) {
		t.Errorf("main.tf = %+v", main.WorkflowFile)
	}
	var handler model.WorkflowFileContent
	e.mustCall(http.StatusOK, "GET", filePath(wf.ID, "src/handler.py"), nil, &handler)
	orig, _ := os.ReadFile(filepath.Join(fixtureRepo(t), "envs", "dev", "src", "handler.py"))
	if handler.Content != string(orig) || handler.Language != "python" {
		t.Errorf("handler.py = %+v %q", handler.WorkflowFile, handler.Content)
	}
}

func TestWorkflowFilesBinaryDetection(t *testing.T) {
	e := newEnv(t)
	wf := importDev(t, e)
	assets := e.st.AssetsDir(wf.ID)
	// UTF-8 text whose sniff window cuts a multi-byte rune stays text.
	cut := strings.Repeat("a", sniffLen-1) + "é tail\n"
	for name, data := range map[string]string{
		"bin/nul.dat":    "text\x00more",
		"bin/latin1.txt": "caf\xe9\n",
		"text/cut.txt":   cut,
		"text/empty.txt": "",
	} {
		p := filepath.Join(assets, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := listWorkflowFiles(t, e, wf.ID)
	for name, binary := range map[string]bool{"bin/nul.dat": true, "bin/latin1.txt": true, "text/cut.txt": false, "text/empty.txt": false} {
		if f := files[name]; f.Binary != binary || f.Editable == binary {
			t.Errorf("%s = %+v, want binary %v", name, f, binary)
		}
	}

	var res struct {
		Error, Code, Download string
		Size                  int64
	}
	if got := e.call("GET", filePath(wf.ID, "bin/nul.dat"), nil, &res); got != http.StatusUnsupportedMediaType {
		t.Fatalf("binary content = %d", got)
	}
	if res.Code != "binary" || res.Size != 9 || res.Download == "" {
		t.Errorf("binary error = %+v", res)
	}
	raw, err := http.Get(e.srv.URL + res.Download)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Body.Close()
	body, _ := io.ReadAll(raw.Body)
	if raw.StatusCode != http.StatusOK || string(body) != "text\x00more" || raw.Header.Get("Content-Type") != "application/octet-stream" ||
		!strings.Contains(raw.Header.Get("Content-Disposition"), `filename=nul.dat`) {
		t.Errorf("download = %d %q %v", raw.StatusCode, body, raw.Header)
	}
	if got := e.call("PUT", filePath(wf.ID, "bin/nul.dat"), map[string]string{"content": "x"}, nil); got != http.StatusConflict {
		t.Errorf("PUT binary = %d, want 409", got)
	}
}

func TestWorkflowFilesRejectsPathTraversal(t *testing.T) {
	e := newEnv(t)
	wf := importDev(t, e)
	assets := e.st.AssetsDir(wf.ID)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(assets, "src", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(assets, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(assets, "src"), filepath.Join(assets, "insidelink")); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"", ".", "..", "../x", "src/../../secret.txt", "/etc/passwd", "./main.tf", "src//handler.py", "src/./handler.py", `src\handler.py`, "src/handler.py/", "a\x00b"} {
		for _, method := range []string{"GET", "PUT"} {
			var body any
			if method == "PUT" {
				body = map[string]string{"content": "pwned"}
			}
			if got := e.call(method, filePath(wf.ID, p), body, nil); got != http.StatusBadRequest {
				t.Errorf("%s %q = %d, want 400", method, p, got)
			}
		}
	}
	// Symlinks are not part of an export: not listed, not readable, not writable.
	for _, p := range []string{"src/link.txt", "linkdir/secret.txt", "insidelink/handler.py", "nope.txt"} {
		if got := e.call("GET", filePath(wf.ID, p), nil, nil); got != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, got)
		}
		if got := e.call("PUT", filePath(wf.ID, p), map[string]string{"content": "pwned"}, nil); got != http.StatusNotFound {
			t.Errorf("PUT %s = %d, want 404", p, got)
		}
		if res, err := http.Get(e.srv.URL + "/api/v1/workflows/" + wf.ID + "/files/raw?path=" + url.QueryEscape(p)); err != nil || res.StatusCode != http.StatusNotFound {
			t.Errorf("raw %s = %v %v", p, res.StatusCode, err)
		}
	}
	files := listWorkflowFiles(t, e, wf.ID)
	for p := range files {
		if strings.Contains(p, "link") {
			t.Errorf("symlink listed: %s", p)
		}
	}
	if data, _ := os.ReadFile(outside); string(data) != "secret" {
		t.Errorf("outside file changed: %q", data)
	}
}

func TestWorkflowFilesEditAssetRoundTrip(t *testing.T) {
	e := newEnv(t)
	wf := importDev(t, e)
	const code = "def handler(event, context):\n    return {\"statusCode\": 201}\n"

	var saved struct {
		File     model.WorkflowFile
		Workflow model.Workflow
	}
	e.mustCall(http.StatusOK, "PUT", filePath(wf.ID, "src/handler.py"), map[string]string{"content": code}, &saved)
	if saved.File.Path != "src/handler.py" || saved.File.Size != int64(len(code)) || !saved.File.Editable {
		t.Errorf("saved file = %+v", saved.File)
	}
	if !saved.Workflow.UpdatedAt.After(wf.UpdatedAt) {
		t.Errorf("updatedAt not bumped: %v -> %v", wf.UpdatedAt, saved.Workflow.UpdatedAt)
	}
	var got model.WorkflowFileContent
	e.mustCall(http.StatusOK, "GET", filePath(wf.ID, "src/handler.py"), nil, &got)
	if got.Content != code {
		t.Errorf("content = %q", got.Content)
	}
	dir := t.TempDir()
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": dir}, nil)
	if data, _ := os.ReadFile(filepath.Join(dir, "src", "handler.py")); string(data) != code {
		t.Errorf("exported handler.py = %q", data)
	}
	// Module files are assets too.
	e.mustCall(http.StatusOK, "PUT", filePath(wf.ID, "modules/subnets/main.tf"), map[string]string{"content": "# edited\n"}, nil)
	if data, _ := os.ReadFile(filepath.Join(e.st.AssetsDir(wf.ID), "modules", "subnets", "main.tf")); string(data) != "# edited\n" {
		t.Errorf("module file = %q", data)
	}

	for _, c := range []struct {
		path, content string
		status        int
	}{
		{"main.tf", "# generated files go through PUT /terraform", http.StatusConflict},
		{"terraform.tfvars", "region = \"x\"\n", http.StatusConflict},
		{".terraform.lock.hcl", "", http.StatusConflict},
		{"src/new.py", "print(1)\n", http.StatusNotFound},
		{"src/handler.py", "bad\x00", http.StatusBadRequest},
		{"src/handler.py", strings.Repeat("x", maxTextFile+1), http.StatusRequestEntityTooLarge},
	} {
		if got := e.call("PUT", filePath(wf.ID, c.path), map[string]string{"content": c.content}, nil); got != c.status {
			t.Errorf("PUT %s = %d, want %d", c.path, got, c.status)
		}
	}
	if got := e.call("PUT", filePath(wf.ID, "src/handler.py"), map[string]any{}, nil); got != http.StatusBadRequest {
		t.Errorf("PUT without content = %d", got)
	}
	e.mustCall(http.StatusOK, "GET", filePath(wf.ID, "src/handler.py"), nil, &got)
	if got.Content != code {
		t.Errorf("rejected PUTs changed the file: %q", got.Content)
	}
}

func TestPutTerraformLocatesSyntaxErrors(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "x"}, &wf)
	var res struct {
		Diagnostics []model.Diagnostic
	}
	got := e.call("PUT", "/workflows/"+wf.ID+"/terraform", map[string]any{"files": map[string]string{
		"main.tf": "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \n}\n",
	}}, &res)
	if got != http.StatusBadRequest || len(res.Diagnostics) == 0 {
		t.Fatalf("PUT = %d %+v", got, res.Diagnostics)
	}
	if d := res.Diagnostics[0]; d.File != "main.tf" || d.Line != 2 {
		t.Errorf("diagnostic = %+v, want main.tf:2", d)
	}
}
