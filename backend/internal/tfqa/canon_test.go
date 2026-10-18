package tfqa

import (
	"path/filepath"
	"strings"
	"testing"
)

// The comparison must ignore layout but catch every real difference;
// otherwise the fidelity checks prove nothing.
func TestCompareDirs(t *testing.T) {
	orig := map[string][]byte{
		"main.tf": []byte(`# queue
resource "aws_sqs_queue" "q" {
  name = "q"
  tags = { a = 1, b = 2 }
  lifecycle {
    ignore_changes = [tags]
  }
}

locals { x = 1 }

module "m" {
  source = "../../mod"
}
`),
		"other.tf": []byte("locals {\n  y = 2\n}\n"),
	}
	mod := map[string][]byte{"main.tf": []byte("variable \"v\" {\n  default = 1 // one\n}\n")}

	write := func(files, module map[string][]byte, moduleDir string) string {
		root := t.TempDir()
		writeTree(t, filepath.Join(root, "a", "b"), files)
		writeTree(t, filepath.Join(root, filepath.FromSlash(moduleDir)), module)
		return filepath.Join(root, "a", "b")
	}
	o := write(orig, mod, "mod")

	// Same configuration: other file split, block order, attribute order,
	// whitespace, merged locals, module copied elsewhere.
	same := map[string][]byte{
		"versions.tf": []byte("locals {\n  y = 2\n  x = 1\n}\n"),
		"main.tf": []byte(`module "m" { source = "./modules/m" }

# queue
resource "aws_sqs_queue" "q" {
  tags = {
    a = 1, b = 2
  }
  name = "q"

  lifecycle { ignore_changes = [tags] }
}
`),
	}
	res, err := CompareDirs(o, write(same, mod, "a/b/modules/m"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("equivalent configuration reported different: %v %v", res.Semantic, res.Comments)
	}

	for _, tc := range []struct {
		name   string
		edit   func(files, module map[string][]byte)
		expect string
	}{
		{"attribute value", func(f, _ map[string][]byte) {
			f["main.tf"] = []byte(strings.Replace(string(f["main.tf"]), `name = "q"`, `name = "r"`, 1))
		}, `resource "aws_sqs_queue" "q" differs`},
		{"missing block", func(f, _ map[string][]byte) {
			f["versions.tf"] = []byte("locals {\n  y = 2\n  x = 1\n}\n")
			f["main.tf"] = []byte("# queue\nmodule \"m\" { source = \"./modules/m\" }\n")
		}, `resource "aws_sqs_queue" "q": missing from export`},
		{"nested block attribute", func(f, _ map[string][]byte) {
			f["main.tf"] = []byte(strings.Replace(string(f["main.tf"]), "lifecycle { ignore_changes = [tags] }", "lifecycle { ignore_changes = [name] }", 1))
		}, "differs"},
		{"duplicate local", func(f, _ map[string][]byte) { f["extra.tf"] = []byte("locals {\n  x = 1\n}\n") }, `duplicate locals attribute "x"`},
		{"module content", func(_, m map[string][]byte) { m["main.tf"] = []byte("variable \"v\" {\n  default = 2 // one\n}\n") }, `module "m": variable "v" differs`},
		{"lost comment", func(f, _ map[string][]byte) {
			f["main.tf"] = []byte(strings.Replace(string(f["main.tf"]), "# queue\n", "", 1))
		}, "comment lost (x1): # queue"},
		{"override file", func(f, _ map[string][]byte) { f["override.tf"] = []byte("resource \"aws_sqs_queue\" \"q\" {}\n") }, "unexpected override file override.tf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string][]byte{}
			for k, v := range same {
				files[k] = v
			}
			module := map[string][]byte{}
			for k, v := range mod {
				module[k] = v
			}
			tc.edit(files, module)
			res, err := CompareDirs(o, write(files, module, "a/b/modules/m"))
			if err != nil {
				t.Fatal(err)
			}
			all := strings.Join(append(res.Semantic, res.Comments...), "\n")
			if !strings.Contains(all, tc.expect) {
				t.Errorf("differences %q do not mention %q", all, tc.expect)
			}
		})
	}
}
