package compose

import (
	"embed"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Templates and build starters live in directories the Go tool ignores
// (leading "_"); a file named "<x>.nimbo" is written as <x> (go.mod can't
// be embedded under its own name: it would start another module).

//go:embed all:_templates all:_starters
var assets embed.FS

var templates = []model.Template{
	{ID: "docker-nginx-static", Name: "Static site (NGINX)", Description: "A static website served by NGINX, mounted from site/", Difficulty: "beginner", Tags: []string{"nginx", "static"}},
	{ID: "docker-node-postgres-redis", Name: "Node.js API + PostgreSQL + Redis", Description: "Express API storing notes in PostgreSQL with a Redis cache", Difficulty: "beginner", Tags: []string{"node", "express", "postgres", "redis"}},
	{ID: "docker-fastapi-postgres", Name: "FastAPI + PostgreSQL", Description: "Python FastAPI service with PostgreSQL and interactive docs", Difficulty: "beginner", Tags: []string{"python", "fastapi", "postgres"}},
	{ID: "docker-go-nginx", Name: "Go API + NGINX reverse proxy", Description: "Multi-stage Go build behind an NGINX reverse proxy", Difficulty: "intermediate", Tags: []string{"go", "nginx", "multi-stage"}},
	{ID: "docker-spring-mysql", Name: "Spring Boot + MySQL", Description: "Java Spring Boot API built with Maven, backed by MySQL", Difficulty: "intermediate", Tags: []string{"java", "spring", "mysql"}},
	{ID: "docker-dotnet-postgres", Name: ".NET API + PostgreSQL", Description: "ASP.NET Core minimal API with Npgsql and PostgreSQL", Difficulty: "intermediate", Tags: []string{"dotnet", "csharp", "postgres"}},
	{ID: "docker-wordpress-mysql", Name: "WordPress + MySQL", Description: "WordPress with a MySQL database and persistent volumes", Difficulty: "beginner", Tags: []string{"wordpress", "mysql", "cms"}},
	{ID: "docker-rabbitmq-worker", Name: "RabbitMQ producer + worker", Description: "An HTTP producer publishing jobs to RabbitMQ and a worker consuming them", Difficulty: "intermediate", Tags: []string{"rabbitmq", "python", "queue"}},
	{ID: "docker-reverse-proxy", Name: "Reverse proxy (NGINX)", Description: "NGINX accepts every client request on one address and forwards /api/ to an api service and everything else to a web service, which stay hidden on an internal network; runs locally with Docker.", Difficulty: "beginner", Tags: []string{"nginx", "go", "reverse-proxy"}, Category: model.TemplateCategoryEdgePatterns, Pattern: model.PatternReverseProxy},
	{ID: "docker-api-gateway", Name: "API gateway (Kong)", Description: "Kong is the single entry point that rejects requests without an API key (401), rate limits each consumer (429), routes /users and /orders to their services and /summary to an aggregating one; runs locally with Docker.", Difficulty: "intermediate", Tags: []string{"kong", "go", "api-gateway"}, Category: model.TemplateCategoryEdgePatterns, Pattern: model.PatternAPIGateway},
	{ID: "docker-load-balancer", Name: "Load balancer (HAProxy)", Description: "HAProxy gives three replicas of an app one address, spreads requests round-robin and health-checks each replica so a failed one stops getting traffic; runs locally with Docker.", Difficulty: "beginner", Tags: []string{"haproxy", "go", "load-balancer"}, Category: model.TemplateCategoryEdgePatterns, Pattern: model.PatternLoadBalancer},
}

func init() {
	for i := range templates {
		templates[i].Kind = model.WorkflowDocker
	}
}

// Templates lists the docker templates.
func Templates() []model.Template { return templates }

// TemplateFiles returns the files of a docker template (nil, false for an
// unknown ID).
func TemplateFiles(id string) (map[string][]byte, bool) {
	for _, t := range templates {
		if t.ID == id {
			return readTree("_templates/" + id), true
		}
	}
	return nil, false
}

// Starters are the Dockerfiles a new build service can start from.
var Starters = map[string]int{"node": 8080, "python": 8080, "go": 8080, "static": 8080, "alpine": 0}

// StarterFiles returns a starter's files and the port it listens on.
func StarterFiles(name string) (map[string][]byte, int, bool) {
	port, ok := Starters[name]
	if !ok {
		return nil, 0, false
	}
	return readTree("_starters/" + name), port, true
}

// metricsStarters serve HTTP server metrics at GET /metrics on their port
// (http_server_request_duration_seconds, the OpenTelemetry HTTP semantic
// conventions in Prometheus form), which Add monitoring scrapes and alerts on.
var metricsStarters = map[string]bool{"node": true, "python": true, "go": true}

// StarterScrapeLabels are the prometheus.io/* labels of a service built
// from starter (nil when the starter serves no metrics).
func StarterScrapeLabels(starter string) map[string]string {
	port := Starters[starter]
	if !metricsStarters[starter] || port == 0 {
		return nil
	}
	return map[string]string{"prometheus.io/scrape": "true", "prometheus.io/port": strconv.Itoa(port), "prometheus.io/path": "/metrics"}
}

func readTree(root string) map[string][]byte {
	files := map[string][]byte{}
	_ = fs.WalkDir(assets, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		if base := path.Base(rel); strings.HasSuffix(base, ".nimbo") {
			rel = strings.TrimSuffix(rel, ".nimbo")
		}
		files[rel] = data
		return nil
	})
	return files
}
