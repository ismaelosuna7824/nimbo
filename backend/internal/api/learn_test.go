package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func learnChallengeOp(id, provider, level, title, tags string) string {
	adaptations := "{}"
	if provider == model.LearnProviderGlobal {
		adaptations = "{ aws: Use Amazon S3 server-side encryption. }"
	}
	return `  - op: upsert
    kind: challenge
    id: ` + id + `
    data:
      provider: ` + provider + `
      level: ` + level + `
      title: ` + title + `
      summary: Practice ` + title + `
      minutes: 10
      tags: [` + tags + `]
      runtime: local
      problem: Make ` + title + ` work.
      requirements: [It works]
      hints: [Read the docs]
      solution: Do it.
      starter: { templateId: docker-nginx-static }
      checks:
        - { id: valid, label: The project is valid, type: validate }
      adaptations: ` + adaptations + `
      sources:
        - { title: Compose file reference, url: "https://docs.docker.com/reference/compose-file/" }
`
}

func learnFixture() fstest.MapFS {
	roadmap := `  - op: upsert
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
`
	return fstest.MapFS{
		"20260101000000_docker_basics.yaml": {Data: []byte("description: Docker basics\noperations:\n" +
			learnChallengeOp("docker-first", "docker", "beginner", "First container", "containers") +
			learnChallengeOp("docker-cache", "docker", "advanced", "Cache aside", "cache, redis") + roadmap)},
		"20260102000000_global_basics.yaml": {Data: []byte("description: Global\noperations:\n" +
			learnChallengeOp("global-encrypt", "global", "intermediate", "Encrypt at rest", "security"))},
	}
}

func TestLearnAPI(t *testing.T) {
	e := newEnvWith(t, "", learnFixture())

	var overview model.LearnOverview
	e.mustCall(http.StatusOK, "GET", "/learn", nil, &overview)
	if len(overview.Status.Errors) != 0 || overview.Status.Applied != 2 {
		t.Fatalf("status = %+v", overview.Status)
	}
	if len(overview.Providers) != len(model.LearnProviders) {
		t.Fatalf("providers = %d", len(overview.Providers))
	}
	for _, p := range overview.Providers {
		switch p.ID {
		case "docker":
			if !p.HasRoadmap || p.Challenges != 2 {
				t.Fatalf("docker = %+v", p)
			}
		case "global":
			if p.HasRoadmap || p.Challenges != 1 {
				t.Fatalf("global = %+v", p)
			}
		}
	}

	var rm model.Roadmap
	e.mustCall(http.StatusOK, "GET", "/learn/providers/docker/roadmap", nil, &rm)
	if rm.ID != "docker-roadmap" || rm.Levels[0].Steps[0].Challenges[0] != "docker-first" {
		t.Fatalf("roadmap = %+v", rm)
	}
	e.mustCall(http.StatusNotFound, "GET", "/learn/providers/aws/roadmap", nil, nil)
	e.mustCall(http.StatusBadRequest, "GET", "/learn/providers/heroku/roadmap", nil, nil)

	filters := []struct {
		query string
		want  []string
	}{
		{"", []string{"docker-first", "docker-cache", "global-encrypt"}},
		{"?provider=docker", []string{"docker-first", "docker-cache"}},
		{"?provider=docker&level=advanced", []string{"docker-cache"}},
		{"?level=intermediate", []string{"global-encrypt"}},
		{"?tag=redis", []string{"docker-cache"}},
		{"?q=container", []string{"docker-first"}},
		{"?q=encrypt+rest", []string{"global-encrypt"}},
		{"?provider=aws", []string{}},
	}
	for _, f := range filters {
		var list []model.ChallengeSummary
		e.mustCall(http.StatusOK, "GET", "/learn/challenges"+f.query, nil, &list)
		got := []string{}
		for _, c := range list {
			got = append(got, c.ID)
		}
		if !slices.Equal(got, f.want) {
			t.Errorf("GET /learn/challenges%s = %v, want %v", f.query, got, f.want)
		}
	}
	e.mustCall(http.StatusBadRequest, "GET", "/learn/challenges?level=guru", nil, nil)
	e.mustCall(http.StatusBadRequest, "GET", "/learn/challenges?provider=heroku", nil, nil)

	var d model.ChallengeDetail
	e.mustCall(http.StatusOK, "GET", "/learn/challenges/docker-first", nil, &d)
	if d.Challenge.Title != "First container" || d.Progress.Status != model.LearnStatusNotStarted || !slices.Equal(d.Steps, []string{"beginner: Images"}) {
		t.Fatalf("detail = %+v", d)
	}
	e.mustCall(http.StatusNotFound, "GET", "/learn/challenges/docker-nope", nil, nil)

	var p model.ChallengeProgress
	e.mustCall(http.StatusOK, "PUT", "/learn/progress/docker-first", map[string]string{"status": "in_progress"}, &p)
	if p.Status != model.LearnStatusInProgress || p.StartedAt == nil {
		t.Fatalf("progress = %+v", p)
	}
	e.mustCall(http.StatusBadRequest, "PUT", "/learn/progress/docker-first", map[string]string{"status": "completed"}, nil)
	e.mustCall(http.StatusNotFound, "PUT", "/learn/progress/docker-nope", map[string]string{"status": "in_progress"}, nil)
	var all []model.ChallengeProgress
	e.mustCall(http.StatusOK, "GET", "/learn/progress", nil, &all)
	if len(all) != 1 || all[0].ChallengeID != "docker-first" {
		t.Fatalf("all progress = %+v", all)
	}
	var list []model.ChallengeSummary
	e.mustCall(http.StatusOK, "GET", "/learn/challenges?provider=docker&level=beginner", nil, &list)
	if list[0].Progress.Status != model.LearnStatusInProgress {
		t.Fatalf("list progress = %+v", list[0].Progress)
	}
	var reset model.ChallengeProgress
	e.mustCall(http.StatusOK, "PUT", "/learn/progress/docker-first", map[string]string{"status": "not_started"}, &reset)
	if reset.Status != model.LearnStatusNotStarted || reset.StartedAt != nil {
		t.Fatalf("reset = %+v", reset)
	}
}

func TestLearnAPIReportsBrokenContent(t *testing.T) {
	fsys := learnFixture()
	fsys["20260103000000_docker_broken.yaml"] = &fstest.MapFile{Data: []byte("description: Broken\noperations:\n  - { op: upsert, kind: challenge, id: docker-broken, data: { title: x } }\n")}
	e := newEnvWith(t, "", fsys)
	var overview model.LearnOverview
	e.mustCall(http.StatusOK, "GET", "/learn", nil, &overview)
	if len(overview.Status.Errors) != 1 || !strings.Contains(overview.Status.Errors[0], "20260103000000_docker_broken.yaml") || overview.Status.Pending != 1 {
		t.Fatalf("status = %+v", overview.Status)
	}
	// What applied is still served.
	var list []model.ChallengeSummary
	e.mustCall(http.StatusOK, "GET", "/learn/challenges", nil, &list)
	if len(list) != 3 {
		t.Fatalf("challenges = %d", len(list))
	}
}
