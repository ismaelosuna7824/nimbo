package compose

import (
	"embed"
	"io/fs"
	"path"
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
