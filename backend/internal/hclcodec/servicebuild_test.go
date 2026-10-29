package hclcodec

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const testPackageDockerfile = "FROM node:22-alpine AS build\nRUN npm run build\nFROM scratch AS package\nCOPY --from=build /src/out/ /\n"

func TestServiceBuilds(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  []string
	}{
		// archive_file zips for any cloud's functions: AWS Lambda, Azure
		// Functions zip deploy, GCP Cloud Functions (bucket object), OCI.
		"zips per cloud": {
			files: map[string]string{
				"main.tf": `
data "archive_file" "lambda" {
  type        = "zip"
  source_dir  = "${path.module}/services/lambda/dist"
  output_path = "${path.module}/build/lambda.zip"
}
data "archive_file" "azfn" {
  type        = "zip"
  source_dir  = "${path.root}/services/azfn/dist"
  output_path = "${path.module}/build/azfn.zip"
}
resource "azurerm_linux_function_app" "fn" {
  zip_deploy_file = data.archive_file.azfn.output_path
}
data "archive_file" "gcf" {
  type        = "zip"
  source_dir  = "services/gcf/dist"
  output_path = "build/gcf.zip"
}
resource "google_storage_bucket_object" "src" {
  source = data.archive_file.gcf.output_path
}
`,
				"services/lambda/Dockerfile": testPackageDockerfile,
				"services/azfn/Dockerfile":   testPackageDockerfile,
				"services/gcf/Dockerfile":    testPackageDockerfile,
			},
			want: []string{"services/azfn", "services/gcf", "services/lambda"},
		},
		// Static files uploaded one by one (S3, Azure Blob, GCS, OCI
		// Object Storage) read dist through fileset and paths inside it.
		"fileset uploads": {
			files: map[string]string{
				"web.tf": `
resource "google_storage_bucket_object" "site" {
  for_each = fileset("${path.module}/services/web/dist", "**")
  source   = "${path.module}/services/web/dist/${each.value}"
}
`,
				"services/web/Dockerfile": testPackageDockerfile,
			},
			want: []string{"services/web"},
		},
		"modules, for_each and unknown prefixes": {
			files: map[string]string{
				"main.tf": `module "m" { source = "./modules/m" }`,
				"modules/m/main.tf": `
data "archive_file" "w" {
  source_dir = "${path.module}/../../services/worker/dist"
}
data "archive_file" "each" {
  for_each   = toset(["a", "b"])
  source_dir = "${path.module}/services/${each.key}/dist"
}
data "archive_file" "tail" {
  source_dir = "${var.root}/services/tail/dist"
}
`,
				"services/worker/Dockerfile":      testPackageDockerfile,
				"services/tail/Dockerfile":        testPackageDockerfile,
				"modules/m/services/a/Dockerfile": "from --platform=$BUILDPLATFORM golang:1.25 as build\nFROM scratch as Package # deployable\n",
				"modules/m/services/b/Dockerfile": "FROM golang:1.25\n",
			},
			want: []string{"modules/m/services/a", "services/tail", "services/worker"},
		},
		"nothing to build": {
			files: map[string]string{
				"main.tf": `
data "archive_file" "py" {
  source_dir = "${path.module}/services/py/src"
}
data "archive_file" "outside" {
  source_dir = "${path.module}/../services/out/dist"
}
resource "x" "y" {
  source = "services/unused/src/index.mjs"
}
`,
				"broken.tf":                  `data "archive_file" "x" {`,
				"services/py/Dockerfile":     testPackageDockerfile,
				"services/unused/Dockerfile": testPackageDockerfile,
				"services/out/Dockerfile":    testPackageDockerfile,
				"services/plain/Dockerfile":  "FROM node:22 AS build\n",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := map[string][]byte{}
			for p, s := range tc.files {
				files[p] = []byte(s)
			}
			if got := ServiceBuilds(files); !slices.Equal(got, tc.want) {
				t.Fatalf("builds = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestServiceBuildScript(t *testing.T) {
	got := ServiceBuildScript([]string{"services/api", "modules/x/services/fn"})
	want := "rm -rf services/api/dist\n" +
		"docker build --target package --output type=local,dest=services/api/dist services/api\n" +
		"rm -rf modules/x/services/fn/dist\n" +
		"docker build --target package --output type=local,dest=modules/x/services/fn/dist modules/x/services/fn"
	if got != want {
		t.Fatalf("script =\n%s", got)
	}
}

// An export with service builds documents them (README), ignores their
// output (.gitignore) and, in the environments layout, builds them before
// plan and apply (Makefile); without builds none of that appears.
func TestScaffoldServiceBuilds(t *testing.T) {
	cmd := "docker build --target package --output type=local,dest=services/api/dist services/api"
	single := largeProject()
	single.Layout = model.LayoutSingle
	env := awsEnvWorkflow(t)
	envBuild := "modules/" + ServiceName(env) + "/services/api"
	envCmd := "docker build --target package --output type=local,dest=" + envBuild + "/dist " + envBuild

	out := Scaffold(single, testResourceFile, Builds{Dists: []string{"services/api"}})
	readme, ignore := string(out[ReadmeFile]), string(out[GitignoreFile])
	if !strings.Contains(readme, "## Build the services") || !strings.Contains(readme, "rm -rf services/api/dist\n"+cmd+"\n") ||
		strings.Index(readme, "## Build the services") > strings.Index(readme, "## Usage") || strings.Contains(readme, "make build") {
		t.Errorf("single README:\n%s", readme)
	}
	if !strings.Contains(ignore, "\n**/services/*/dist/\n") {
		t.Errorf("single .gitignore:\n%s", ignore)
	}

	out = Scaffold(env, testResourceFile, Builds{Dists: []string{envBuild}})
	readme, ignore, mk := string(out[ReadmeFile]), string(out[GitignoreFile]), string(out[MakefileFile])
	if !strings.Contains(readme, envCmd) || !strings.Contains(readme, "`make build` runs these commands") || !strings.Contains(readme, "| `make build` |") {
		t.Errorf("environments README:\n%s", readme)
	}
	if !strings.Contains(ignore, "**/services/*/dist/") {
		t.Errorf("environments .gitignore:\n%s", ignore)
	}
	for _, s := range []string{".PHONY: init plan apply fmt validate build\n", "build:\n\trm -rf " + envBuild + "/dist\n\t" + envCmd + "\n",
		"plan: build\n\t$(TERRAFORM)", "apply: build\n\t$(TERRAFORM)"} {
		if !strings.Contains(mk, s) {
			t.Errorf("Makefile lacks %q:\n%s", s, mk)
		}
	}
	if svcReadme := string(out["modules/"+ServiceName(env)+"/"+ReadmeFile]); strings.Contains(svcReadme, "Build the services") {
		t.Errorf("service module README documents the builds:\n%s", svcReadme)
	}

	for _, wf := range []*model.Workflow{single, env} {
		for name, data := range Scaffold(wf, testResourceFile, Builds{}) {
			if s := string(data); strings.Contains(s, "services/") || strings.Contains(s, "docker build") || strings.Contains(s, "build:") {
				t.Errorf("%s mentions service builds without any:\n%s", name, s)
			}
		}
	}
}

func TestServiceImages(t *testing.T) {
	files := map[string][]byte{
		"main.tf": []byte(`
variable "orders_image" {
  default = "nimbo.local/orders:latest"
}
resource "aws_ecs_task_definition" "t" {
  container_definitions = jsonencode([{ name = "api", image = "nimbo.local/orders-api" }])
}
resource "aws_lambda_function" "fn" {
  package_type = "Image"
  image_uri    = "nimbo.local/${var.name}:latest"
}
`),
		"modules/m/main.tf":                    []byte(`locals { worker = "nimbo.local/worker:v2" }`),
		"dev.tfvars":                           []byte(`extra_image = "nimbo.local/ghost:latest"`),
		"services/orders/Dockerfile":           []byte("FROM golang:1.25 AS build\nFROM alpine:3 AS Runtime\nFROM scratch AS package\n"),
		"services/orders-api/Dockerfile":       []byte("FROM node:22\nCMD [\"node\", \"server.js\"]\n"),
		"modules/m/services/worker/Dockerfile": []byte("FROM node:22 AS build\nFROM scratch AS package\n"),
		"modules/x/services/orders/Dockerfile": []byte("FROM busybox AS runtime\n"),
	}
	images, problems := ServiceImages(files)
	want := []ServiceImage{{Name: "orders", Dir: "services/orders", Target: "runtime"}, {Name: "orders-api", Dir: "services/orders-api"}}
	if !slices.Equal(images, want) {
		t.Errorf("images = %+v", images)
	}
	wantProblems := []string{
		"nimbo.local/ghost: no services/ghost/Dockerfile to build it from",
		"modules/m/services/worker/Dockerfile: its last stage is package (the function files); add a stage named runtime for the nimbo.local/worker image",
	}
	if !slices.Equal(problems, wantProblems) {
		t.Errorf("problems = %q", problems)
	}
	if v := ImageVariable("orders-api"); v != "orders_api_image" {
		t.Errorf("ImageVariable = %s", v)
	}
	if got := ImageBuildCommand(want[0], "r/orders:1"); got != "docker build --target runtime -t r/orders:1 services/orders" {
		t.Errorf("ImageBuildCommand = %s", got)
	}
	if got := ImageBuildCommand(want[1], "r/a:1"); got != "docker build -t r/a:1 services/orders-api" {
		t.Errorf("ImageBuildCommand = %s", got)
	}
}

// Images get build-and-push commands with their variable; a missing
// variable is called out.
func TestScaffoldServiceImages(t *testing.T) {
	wf := largeProject()
	wf.Layout = model.LayoutSingle
	wf.Variables = append(wf.Variables, model.NamedBlock{Name: "orders_image"})
	images := []ServiceImage{{Name: "orders", Dir: "services/orders", Target: "runtime"}, {Name: "web-ui", Dir: "services/web-ui"}}
	readme := string(Scaffold(wf, testResourceFile, Builds{Images: images})[ReadmeFile])
	for _, s := range []string{"## Build the services", "docker build --target runtime -t $REGISTRY/orders:$TAG services/orders\ndocker push $REGISTRY/orders:$TAG\nexport TF_VAR_orders_image=$REGISTRY/orders:$TAG\n",
		"docker build -t $REGISTRY/web-ui:$TAG services/web-ui", "export TF_VAR_web_ui_image=$REGISTRY/web-ui:$TAG", "IMAGE_REGISTRY",
		"Declare the variable `web_ui_image`"} {
		if !strings.Contains(readme, s) {
			t.Errorf("README lacks %q:\n%s", s, readme)
		}
	}
	if strings.Contains(readme, "Declare the variable `orders_image`") || strings.Contains(readme, "dist/") {
		t.Errorf("README:\n%s", readme)
	}
	if ignore := string(Scaffold(wf, testResourceFile, Builds{Images: images})[GitignoreFile]); strings.Contains(ignore, "services/") {
		t.Errorf(".gitignore for images only:\n%s", ignore)
	}
}
