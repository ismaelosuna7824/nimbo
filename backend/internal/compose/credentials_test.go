package compose

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// findingsOf lists a rule's findings as severity@file:line.
func findingsOf(p *Project, code string) []string {
	var out []string
	for _, d := range p.Diagnostics {
		if d.Code == code {
			out = append(out, string(d.Severity)+"@"+d.File+":"+itoa(d.Line))
		}
	}
	slices.Sort(out)
	return out
}

func TestSecretFileCopied(t *testing.T) {
	p := open(t, map[string]string{
		"compose.yaml": `services:
  web:
    build: ./web
  api:
    build: ./api
  ok:
    build: ./ok
  multi:
    build: ./multi
`,
		// Named credential files, and COPY . with no .dockerignore.
		"web/Dockerfile": `FROM node:22-alpine
WORKDIR /app
COPY package.json .npmrc ./
COPY id_rsa /root/.ssh/id_rsa
COPY . .
CMD ["node", "server.js"]
`,
		"web/package.json": "{}",
		"web/.npmrc":       "//registry.npmjs.org/:_authToken=npm_abc123\n",
		"web/id_rsa":       "-----BEGIN OPENSSH PRIVATE KEY-----\n",
		"web/.env":         "DB_PASSWORD=hunter2\n",
		// The .dockerignore misses .env.production.
		"api/Dockerfile": `FROM node:22-alpine
COPY . .
CMD ["node", "server.js"]
`,
		"api/.dockerignore":     ".env\n",
		"api/.env":              "API_TOKEN=abc\n",
		"api/.env.production":   "API_TOKEN=prod\n",
		"api/.env.example":      "API_TOKEN=changeme\n",
		"api/production.env.md": "not an env file",
		// Configurations without credentials, ignored env files, examples,
		// and a ${VAR} token reference are fine.
		"ok/Dockerfile": `FROM node:22-alpine
COPY .npmrc .yarnrc.yml ./
COPY . .
CMD ["node", "server.js"]
`,
		"ok/.npmrc":        "ignore-scripts=true\n//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n",
		"ok/.yarnrc.yml":   "nodeLinker: node-modules\n",
		"ok/.env":          "PORT=3000\nDEBUG=true\n",
		"ok/.env.local":    "DB_PASSWORD=x\n",
		"ok/.dockerignore": ".env*\n!.env\n",
		// A build stage's files don't reach the image; the runtime stage's do.
		"multi/Dockerfile": `FROM node:22-alpine AS build
COPY .npmrc .
COPY . .
FROM build AS base
FROM nginx:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY .netrc /root/.netrc
`,
		"multi/.npmrc": "_auth=dXNlcjpwYXNz\n",
		"multi/.netrc": "machine example.com login u password p\n",
		"multi/.env":   "SECRET_KEY=s\n",
	})
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	want := []string{
		"warning@api/Dockerfile:2",
		"warning@multi/Dockerfile:7",
		"warning@web/Dockerfile:3",
		"warning@web/Dockerfile:4",
		"warning@web/Dockerfile:5",
	}
	if got := findingsOf(p, "dk_secret_file"); !slices.Equal(got, want) {
		t.Fatalf("findings:\n got %v\nwant %v", got, want)
	}
	for _, d := range p.Diagnostics {
		if d.Code != "dk_secret_file" {
			continue
		}
		switch {
		case d.File == "api/Dockerfile" && (!strings.Contains(d.Message, ".env.production") || strings.Contains(d.Message, ".env.example") || !strings.Contains(d.Fix, "api/.dockerignore") || !strings.Contains(d.Fix, ".env*")):
			t.Errorf("api: %s / %s", d.Message, d.Fix)
		case d.File == "web/Dockerfile" && d.Line == 5 && (!strings.Contains(d.Message, ".env") || !strings.Contains(d.Message, "no .dockerignore")):
			t.Errorf("web COPY .: %s", d.Message)
		case d.File == "web/Dockerfile" && d.Line == 3 && (!strings.Contains(d.Message, ".npmrc") || strings.Contains(d.Message, "package.json") || !strings.Contains(d.Fix, "--mount=type=secret")):
			t.Errorf("web .npmrc: %s / %s", d.Message, d.Fix)
		}
	}
}

func TestCredentialFileNames(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, ".env.production": true, "staging.env": true, ".env.example": false, "sample.env": false,
		".env.template": false, "env": false, ".envrc": false, "production.env.md": false,
	} {
		if got := envFile(name); got != want {
			t.Errorf("envFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestNetworkInternal(t *testing.T) {
	cases := []struct {
		name, compose string
		want          []string
	}{
		{"database on the default network with a published app", `services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
  db:
    image: postgres:16
`, []string{"info@compose.yaml:5"}},
		{"internal backend network", `services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
    networks: [default, backend]
  db:
    image: postgres:16
    networks: [backend]
networks:
  backend:
    internal: true
`, nil},
		{"separate network without published services", `services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
    networks: [front]
  worker:
    image: python:3.12-slim
    networks: [back]
  db:
    image: postgres:16
    networks: [back]
networks:
  front: {}
  back: {}
`, nil},
		{"published database is dk_db_published's", `services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
  db:
    image: postgres:16
    ports: ["127.0.0.1:5432:5432"]
`, nil},
		{"nothing published", `services:
  worker:
    image: python:3.12-slim
  db:
    image: postgres:16
`, nil},
		{"external network", `services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
    networks: [shared]
  cache:
    image: redis:7
    networks: [shared]
networks:
  shared:
    external: true
`, nil},
		{"network_mode", `services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
  cache:
    image: redis:7
    network_mode: "service:web"
`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := open(t, map[string]string{"compose.yaml": c.compose})
			if p.Model == nil {
				t.Fatal(p.Diagnostics)
			}
			if got := findingsOf(p, "dk_network_internal"); !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
	p := open(t, map[string]string{"compose.yaml": cases[0].compose})
	for _, d := range p.Diagnostics {
		if d.Code == "dk_network_internal" && (!strings.Contains(d.Message, "shares network default with web") || !strings.Contains(d.Fix, "internal: true") || !strings.Contains(d.Fix, "web:\n    networks: [default, backend]")) {
			t.Errorf("message/fix: %s\n%s", d.Message, d.Fix)
		}
	}
	// Opt-in: quiet with the default settings.
	if slices.ContainsFunc(FilterSettings(slices.Clone(p.Diagnostics), model.BestPracticeSettings{}), func(d model.Diagnostic) bool { return d.Code == "dk_network_internal" }) {
		t.Error("dk_network_internal reported without opting in")
	}
}
