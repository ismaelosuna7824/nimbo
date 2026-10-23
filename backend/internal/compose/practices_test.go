package compose

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// finding is code@file:line for assertions.
func findings(p *Project, cfg model.BestPracticeSettings) []string {
	var out []string
	for _, d := range FilterSettings(slices.Clone(p.Diagnostics), cfg) {
		if d.Severity == model.SeverityError {
			continue
		}
		out = append(out, d.Code+"@"+d.File+":"+itoa(d.Line))
	}
	slices.Sort(out)
	return out
}

func TestPracticesFindEveryBadPractice(t *testing.T) {
	old := sharedFilesOS
	sharedFilesOS = "darwin"
	defer func() { sharedFilesOS = old }()
	p := open(t, map[string]string{
		"compose.yaml": `services:
  web:
    build: ./web
    ports:
      - "3000:3000"
    depends_on:
      - db
    volumes:
      - ./web:/app
      - /var/run/docker.sock:/var/run/docker.sock
    logging:
      driver: json-file
    privileged: true
  db:
    image: postgres:16
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 2s
    restart: always
  api:
    build: ./api
    expose: ["8080"]
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/"]
      interval: 10s
      timeout: 30s
`,
		"web/Dockerfile": `FROM node
ARG NPM_TOKEN
ENV API_KEY=abc123
RUN cd /tmp && echo hi
RUN apt-get update && apt-get install -y curl
RUN apk add curl
RUN pip install flask
ADD app.js /app/
ADD https://example.com/x.tgz /tmp/
WORKDIR /app
COPY . .
RUN npm ci
RUN npm run build
EXPOSE 8080
CMD npm start
`,
		"web/app.js":                 "",
		"web/node_modules/big/x.bin": strings.Repeat("x", 6<<20),
		"api/Dockerfile":             "FROM node:22\nWORKDIR /app\nCOPY package.json ./\nRUN npm install\nCOPY . .\nUSER node\nEXPOSE 8080\nCMD [\"node\", \"server.js\"]\n",
		"api/package.json":           "{}",
		"api/.dockerignore":          "node_modules\n",
		"api/node_modules/big/x.bin": strings.Repeat("x", 6<<20),
	})
	got := findings(p, model.BestPracticeSettings{EnabledRules: []string{"dk_cache_mount", "dk_no_limits"}})
	want := []string{
		"dk_add@web/Dockerfile:8",
		"dk_add@web/Dockerfile:9",
		"dk_anon_volume@compose.yaml:14",
		"dk_apk_cache@web/Dockerfile:6",
		"dk_apt_install@web/Dockerfile:5",
		"dk_bind_deps@compose.yaml:9",
		"dk_cache_mount@api/Dockerfile:4",
		"dk_cache_mount@web/Dockerfile:12",
		"dk_db_published@compose.yaml:17",
		"dk_depends_healthy@compose.yaml:7",
		"dk_docker_sock@compose.yaml:10",
		"dk_dockerignore@compose.yaml:3",
		"dk_expose_mismatch@compose.yaml:5",
		"dk_healthcheck_timing@compose.yaml:19",
		"dk_healthcheck_timing@compose.yaml:27",
		"dk_heavy_base@api/Dockerfile:1",
		"dk_latest_tag@web/Dockerfile:1",
		"dk_layer_order@web/Dockerfile:11",
		"dk_log_rotation@compose.yaml:12",
		"dk_missing_healthcheck@compose.yaml:2",
		"dk_no_init@compose.yaml:22",
		"dk_no_limits@compose.yaml:14",
		"dk_no_limits@compose.yaml:2",
		"dk_no_limits@compose.yaml:22",
		"dk_no_restart@compose.yaml:2",
		"dk_pip_cache@web/Dockerfile:7",
		"dk_privileged@compose.yaml:13",
		"dk_root_user@web/Dockerfile:1",
		"dk_secret_build@web/Dockerfile:2",
		"dk_secret_build@web/Dockerfile:3",
		"dk_shell_form@web/Dockerfile:15",
		"dk_single_stage@web/Dockerfile:13",
		"dk_workdir@web/Dockerfile:4",
	}
	if !slices.Equal(got, want) {
		t.Errorf("findings:\n got %v\nwant %v", got, want)
		for _, d := range p.Diagnostics {
			t.Logf("%s %s:%d %s — %s", d.Code, d.File, d.Line, d.Message, d.Fix)
		}
	}
	// Every advice finding explains itself and links its docs.
	for _, d := range p.Diagnostics {
		if d.Detail == "" || d.Fix == "" || !strings.HasPrefix(d.Docs, "https://") || d.NodeID == "" {
			t.Errorf("%s %s:%d lacks why/fix/docs/node: %+v", d.Code, d.File, d.Line, d)
		}
	}
	// The measured context size is in the message.
	for _, d := range p.Diagnostics {
		if d.Code == "dk_dockerignore" && !strings.Contains(d.Message, "node_modules (6 MB)") {
			t.Errorf("dockerignore message: %s", d.Message)
		}
	}
}

func TestPracticesBindDepsOnlyThroughDockerDesktop(t *testing.T) {
	old := sharedFilesOS
	sharedFilesOS = "linux"
	defer func() { sharedFilesOS = old }()
	p := open(t, map[string]string{
		"compose.yaml":          "services:\n  web:\n    image: node:22-alpine\n    restart: unless-stopped\n    volumes:\n      - ./app:/app\n",
		"app/node_modules/x.js": "",
	})
	if got := findings(p, model.BestPracticeSettings{}); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "dk_bind_deps") }) {
		t.Errorf("linux: %v", got)
	}
	sharedFilesOS = "darwin"
	p = open(t, map[string]string{
		"compose.yaml":          "services:\n  web:\n    image: node:22-alpine\n    restart: unless-stopped\n    volumes:\n      - ./app:/app\n      - deps:/app/node_modules\nvolumes:\n  deps:\n",
		"app/node_modules/x.js": "",
	})
	if got := findings(p, model.BestPracticeSettings{}); len(got) != 0 {
		t.Errorf("node_modules in a volume: %v", got)
	}
}

func TestPracticesQuietOnGoodProjects(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `services:
  api:
    build:
      context: ./api
      target: runtime
    init: true
    ports: ["127.0.0.1:8080:8080"]
    depends_on:
      db: {condition: service_healthy}
    restart: unless-stopped
    logging:
      options: {max-size: 10m}
  db:
    image: postgres:16-alpine
    ports: ["127.0.0.1:5432:5432"]
    volumes: [data:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 5s
    restart: unless-stopped
  migrate:
    image: node:22-alpine
    command: ["node", "migrate.js"]
    depends_on:
      db: {condition: service_healthy}
  waits:
    image: alpine:3.20
    restart: "no"
    depends_on:
      migrate: {condition: service_completed_successfully}
volumes:
  data: {}
`,
		"api/Dockerfile": `# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app .

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/app /app
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s CMD ["/app", "-healthcheck"]
ENTRYPOINT ["/app"]
`,
		"api/go.mod": "module x\n",
		"api/go.sum": "",
	})
	if got := findings(p, model.BestPracticeSettings{}); len(got) != 0 {
		t.Errorf("good project has findings: %v", got)
		for _, d := range p.Diagnostics {
			t.Logf("%s %s:%d %s", d.Code, d.File, d.Line, d.Message)
		}
	}
}

func TestDependsCompletedOnAServer(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `services:
  collector:
    image: otel/opentelemetry-collector-contrib:0.109.0
    restart: unless-stopped
    depends_on:
      cache: {condition: service_completed_successfully}
      seed: {condition: service_completed_successfully}
  cache:
    image: redis:7-alpine
    restart: unless-stopped
  seed:
    image: alpine:3.20
    restart: "no"
`,
	})
	var got []string
	for _, d := range p.Diagnostics {
		if d.Code == "dk_depends_completed" {
			got = append(got, string(d.Severity)+"@"+d.File+":"+itoa(d.Line))
		}
	}
	// The cache never exits; the seed job does.
	if want := []string{"error@compose.yaml:6"}; !slices.Equal(got, want) {
		t.Errorf("dk_depends_completed = %v, want %v", got, want)
	}
}

func TestPracticesBuildTargetOfAToolchainStage(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml":   "services:\n  api:\n    build: {context: ./api, target: builder}\n    restart: unless-stopped\n",
		"api/Dockerfile": "FROM golang:1.23-alpine AS builder\nWORKDIR /src\nCOPY . .\nRUN go build -o /app .\n\nFROM alpine:3.20\nCOPY --from=builder /app /app\nUSER 10001\nCMD [\"/app\"]\n",
		"api/go.mod":     "module x\n",
	})
	for _, d := range p.Diagnostics {
		if d.Code == "dk_single_stage" {
			if d.Severity != model.SeverityInfo || !strings.Contains(d.Fix, "build.target") {
				t.Errorf("target of a build stage: %+v", d)
			}
			return
		}
	}
	t.Errorf("no dk_single_stage: %v", codes(p))
}

func TestStartersFollowThePractices(t *testing.T) {
	for name, port := range Starters {
		files, _, _ := StarterFiles(name)
		tree := map[string]string{}
		for rel, data := range files {
			tree["app/"+rel] = string(data)
		}
		// The service add_service writes for a starter.
		svc := "services:\n  app:\n    build:\n      context: ./app\n    restart: unless-stopped\n"
		if port > 0 {
			svc += "    ports:\n      - \"" + itoa(port) + ":" + itoa(port) + "\"\n"
		}
		if name == "node" || name == "python" {
			svc += "    init: true\n"
		}
		tree["compose.yaml"] = svc
		p := open(t, tree)
		all := model.BestPracticeSettings{EnabledRules: []string{"dk_no_limits", "dk_cache_mount"}}
		for _, f := range findings(p, all) {
			if !strings.HasPrefix(f, "dk_no_limits") {
				t.Errorf("starter %s: %s", name, f)
			}
		}
		if _, ok := files[".dockerignore"]; !ok {
			t.Errorf("starter %s has no .dockerignore", name)
		}
	}
}

func TestFilterSettingsOptInRules(t *testing.T) {
	diags := []model.Diagnostic{
		{Severity: model.SeverityInfo, Code: "dk_hardening"},
		{Severity: model.SeverityInfo, Code: "dk_no_restart"},
		{Severity: model.SeverityError, Code: "dk_yaml"},
	}
	codesOf := func(ds []model.Diagnostic) []string {
		var out []string
		for _, d := range ds {
			out = append(out, d.Code)
		}
		return out
	}
	if got := codesOf(FilterSettings(slices.Clone(diags), model.BestPracticeSettings{})); !slices.Equal(got, []string{"dk_no_restart", "dk_yaml"}) {
		t.Errorf("defaults: %v", got)
	}
	if got := codesOf(FilterSettings(slices.Clone(diags), model.BestPracticeSettings{EnabledRules: []string{"dk_hardening"}, DisabledRules: []string{"dk_no_restart"}})); !slices.Equal(got, []string{"dk_hardening", "dk_yaml"}) {
		t.Errorf("toggled: %v", got)
	}
	if got := codesOf(FilterSettings(slices.Clone(diags), model.BestPracticeSettings{Disabled: true, EnabledRules: []string{"dk_hardening"}})); !slices.Equal(got, []string{"dk_yaml"}) {
		t.Errorf("all off: %v", got)
	}
	for _, r := range Rules() {
		if r.Severity != model.SeverityError && (r.Docs == "" || r.Category == catErrors) {
			t.Errorf("rule %s: docs %q category %q", r.Code, r.Docs, r.Category)
		}
	}
}
