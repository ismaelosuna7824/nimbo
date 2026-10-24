package mcp

import (
	"strings"
	"testing"
	"testing/fstest"
)

// learnFixture is the learn content of the test daemon.
var learnFixture = fstest.MapFS{
	"20260101000000_docker_basics.yaml": {Data: []byte(`description: Docker basics
operations:
  - op: upsert
    kind: challenge
    id: docker-first
    data:
      provider: docker
      level: beginner
      title: First container
      summary: Run a web server in a container.
      minutes: 10
      tags: [containers]
      runtime: local
      problem: Serve the site.
      requirements: [The site answers]
      hints: [Start from the nginx image]
      solution: Publish port 80.
      solutionFiles: { compose.yaml: "services: {}" }
      starter: { templateId: docker-nginx-static }
      checks:
        - { id: valid, label: The project is valid, type: validate }
      sources:
        - { title: Compose file reference, url: "https://docs.docker.com/reference/compose-file/" }
  - op: upsert
    kind: roadmap
    id: docker-roadmap
    data:
      provider: docker
      title: Docker
      summary: Containers to production.
      levels:
        - level: beginner
          title: Basics
          goals: Run containers.
          steps:
            - title: Images
              description: Learn images.
              challenges: [docker-first]
              docs: [{ title: Get started, url: "https://docs.docker.com/get-started/" }]
      sources: [{ title: Docker docs, url: "https://docs.docker.com/" }]
`)},
}

func TestLearnTools(t *testing.T) {
	cs, _ := session(t)

	var overview struct {
		Providers []struct {
			ID         string `json:"id"`
			Challenges int    `json:"challenges"`
			HasRoadmap bool   `json:"hasRoadmap"`
		} `json:"providers"`
	}
	mustTool(t, cs, "list_learning_paths", nil, &overview)
	found := false
	for _, p := range overview.Providers {
		if p.ID == "docker" {
			found = p.Challenges == 1 && p.HasRoadmap
		}
	}
	if !found {
		t.Fatalf("providers = %+v", overview.Providers)
	}

	var roadmap struct {
		Levels []learnLevelView `json:"levels"`
	}
	mustTool(t, cs, "list_learning_paths", map[string]any{"provider": "docker"}, &roadmap)
	step := roadmap.Levels[0].Steps[0]
	if step.Challenges[0].ID != "docker-first" || step.Challenges[0].Title != "First container" || step.Challenges[0].Status != "not_started" || !strings.Contains(step.Docs[0], "https://docs.docker.com/get-started/") {
		t.Fatalf("step = %+v", step)
	}
	if text, isErr := callTool(t, cs, "list_learning_paths", map[string]any{"provider": "aws"}); !isErr || !strings.Contains(text, "no roadmap") {
		t.Fatalf("aws roadmap: %v %s", isErr, text)
	}

	var list []challengeListView
	mustTool(t, cs, "list_challenges", map[string]any{"provider": "docker", "level": "beginner", "query": "web server"}, &list)
	if len(list) != 1 || list[0].ID != "docker-first" {
		t.Fatalf("list = %+v", list)
	}
	mustTool(t, cs, "list_challenges", map[string]any{"level": "expert"}, &list)
	if len(list) != 0 {
		t.Fatalf("expert list = %+v", list)
	}

	text := mustTool(t, cs, "get_challenge", map[string]any{"id": "docker-first"}, nil)
	if strings.Contains(text, "Publish port 80") || strings.Contains(text, "solutionFiles") || !strings.Contains(text, "Start from the nginx image") {
		t.Fatalf("get_challenge must hide the solution by default: %s", text)
	}
	text = mustTool(t, cs, "get_challenge", map[string]any{"id": "docker-first", "include_solution": true}, nil)
	if !strings.Contains(text, "Publish port 80") || !strings.Contains(text, "solutionFiles") {
		t.Fatalf("include_solution: %s", text)
	}
}
