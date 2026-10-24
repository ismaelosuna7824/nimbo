package learn

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// challengeYAML is a valid challenge operation for tests.
func challengeYAML(id, provider, level, title string, tags string, prereqs string) string {
	return `  - op: upsert
    kind: challenge
    id: ` + id + `
    data:
      provider: ` + provider + `
      level: ` + level + `
      title: ` + title + `
      summary: Practice ` + title + `
      minutes: 15
      tags: [` + tags + `]
      prerequisites: [` + prereqs + `]
      runtime: local
      problem: The team needs ` + title + `.
      requirements: [It works]
      hints: [Read the docs]
      solution: Do it.
      starter: { templateId: docker-nginx-static }
      checks:
        - { id: valid, label: The project is valid, type: validate }
      adaptations: ` + adaptations(provider) + `
      sources:
        - { title: Compose file reference, url: "https://docs.docker.com/reference/compose-file/" }
      solvableWithoutCode: false
`
}

func adaptations(provider string) string {
	if provider == model.LearnProviderGlobal {
		return `{ aws: Use Amazon S3. }`
	}
	return `{}`
}

const roadmapYAML = `  - op: upsert
    kind: roadmap
    id: docker-roadmap
    data:
      provider: docker
      title: Docker
      summary: From containers to production compose projects.
      levels:
        - level: beginner
          title: First containers
          goals: Run a container.
          steps:
            - title: Images and containers
              description: Learn images.
              challenges: [docker-first]
              docs:
                - { title: Get started, url: "https://docs.docker.com/get-started/" }
      sources:
        - { title: Docker docs, url: "https://docs.docker.com/" }
`

func migration(desc string, ops ...string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("description: " + desc + "\noperations:\n" + strings.Join(ops, ""))}
}

const (
	file1 = "20260101000000_docker_basics.yaml"
	file2 = "20260102000000_docker_more.yaml"
	file3 = "20260103000000_docker_cleanup.yaml"
)

func baseFS() fstest.MapFS {
	return fstest.MapFS{
		file1: migration("Docker basics",
			challengeYAML("docker-first", "docker", "beginner", "First container", "containers, images", ""),
			roadmapYAML),
	}
}

func open(t *testing.T, path string, fsys fstest.MapFS) *Store {
	t.Helper()
	s := OpenFile(path, Options{Migrations: fsys})
	t.Cleanup(func() { s.Close() })
	return s
}

func noErrors(t *testing.T, s *Store) {
	t.Helper()
	if st := s.Status(); len(st.Errors) > 0 {
		t.Fatalf("unexpected load errors: %v", st.Errors)
	}
}

func ids(list []model.ChallengeSummary) []string {
	out := []string{}
	for _, c := range list {
		out = append(out, c.ID)
	}
	return out
}

func TestFreshApply(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), FileName), baseFS())
	noErrors(t, s)
	if st := s.Status(); st.Applied != 1 || st.Pending != 0 {
		t.Fatalf("status = %+v, want 1 applied", st)
	}
	c, ok, err := s.Challenge("docker-first")
	if err != nil || !ok {
		t.Fatalf("challenge: ok=%v err=%v", ok, err)
	}
	if c.Title != "First container" || c.Starter.TemplateID != "docker-nginx-static" || len(c.Checks) != 1 {
		t.Fatalf("challenge decoded wrong: %+v", c)
	}
	r, ok, err := s.Roadmap("docker")
	if err != nil || !ok || len(r.Levels) != 1 || r.Levels[0].Steps[0].Challenges[0] != "docker-first" {
		t.Fatalf("roadmap: %+v ok=%v err=%v", r, ok, err)
	}
	providers, err := s.Providers()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != len(model.LearnProviders) {
		t.Fatalf("providers = %d", len(providers))
	}
	for _, p := range providers {
		if p.ID == "docker" && (!p.HasRoadmap || p.Challenges != 1 || p.Levels[0].Challenges != 1) {
			t.Fatalf("docker provider = %+v", p)
		}
		if p.ID == "aws" && (p.HasRoadmap || p.Challenges != 0) {
			t.Fatalf("aws provider = %+v", p)
		}
	}
}

func TestIncrementalApplyKeepsProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	fsys := baseFS()
	s := OpenFile(path, Options{Migrations: fsys})
	noErrors(t, s)
	if _, err := s.MarkStarted("docker-first", "wf-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttempt("docker-first", "wf-1", model.ChallengeResult{Passed: true, Goals: []model.ChallengeGoalResult{{ID: "valid", Passed: true}}}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// An update of the challenge plus a new one.
	fsys[file2] = migration("More docker",
		challengeYAML("docker-first", "docker", "beginner", "First container v2", "containers", ""),
		challengeYAML("docker-second", "docker", "intermediate", "Second", "compose", "docker-first"))
	s = open(t, path, fsys)
	noErrors(t, s)
	if st := s.Status(); st.Applied != 2 {
		t.Fatalf("applied = %d, want 2", st.Applied)
	}
	c, _, _ := s.Challenge("docker-first")
	if c.Title != "First container v2" {
		t.Fatalf("update not applied: %q", c.Title)
	}
	p, err := s.Progress("docker-first")
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != model.LearnStatusCompleted || p.Attempts != 1 || !slices.Equal(p.CompletedGoals, []string{"valid"}) || p.CompletedAt == nil || p.WorkflowID != "wf-1" {
		t.Fatalf("progress lost across the upgrade: %+v", p)
	}
	d, ok, err := s.Detail("docker-second")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(d.Prerequisites) != 1 || !d.Prerequisites[0].Completed || d.Prerequisites[0].Title != "First container v2" {
		t.Fatalf("prerequisites = %+v", d.Prerequisites)
	}
	if d, _, _ := s.Detail("docker-first"); !slices.Equal(d.Steps, []string{"beginner: Images and containers"}) {
		t.Fatalf("steps = %v", d.Steps)
	}
}

func TestIdempotentReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := OpenFile(path, Options{Migrations: baseFS(), Now: func() time.Time { return clock }})
	noErrors(t, s)
	s.Close()
	clock = clock.Add(time.Hour)
	s = OpenFile(path, Options{Migrations: baseFS(), Now: func() time.Time { return clock }})
	defer s.Close()
	noErrors(t, s)
	var n int
	var appliedAt string
	if err := s.db.QueryRow(`SELECT count(*), max(applied_at) FROM migrations`).Scan(&n, &appliedAt); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.HasPrefix(appliedAt, "2026-01-01T00:00:00") {
		t.Fatalf("migrations rows = %d applied_at = %s: reopening re-applied", n, appliedAt)
	}
	list, _ := s.Challenges(model.ChallengeFilter{})
	if len(list) != 1 {
		t.Fatalf("challenges = %v", ids(list))
	}
}

func TestChecksumChangeIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	fsys := baseFS()
	open(t, path, fsys).Close()
	fsys[file1] = migration("Docker basics, edited",
		challengeYAML("docker-first", "docker", "beginner", "Edited", "containers", ""), roadmapYAML)
	s := open(t, path, fsys)
	st := s.Status()
	if len(st.Errors) != 1 || !strings.Contains(st.Errors[0], "edited after it was applied") {
		t.Fatalf("errors = %v", st.Errors)
	}
	// The applied content is still served.
	if c, ok, _ := s.Challenge("docker-first"); !ok || c.Title != "First container" {
		t.Fatalf("content after a checksum error: %+v", c)
	}
}

func TestDeleteOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	fsys := baseFS()
	fsys[file2] = migration("Second", challengeYAML("docker-second", "docker", "intermediate", "Second", "compose", ""))
	s := OpenFile(path, Options{Migrations: fsys})
	noErrors(t, s)
	if _, err := s.MarkStarted("docker-second", "wf-2"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	fsys[file3] = migration("Remove second", "  - { op: delete, kind: challenge, id: docker-second }\n")
	s = open(t, path, fsys)
	noErrors(t, s)
	if _, ok, _ := s.Challenge("docker-second"); ok {
		t.Fatal("deleted challenge still present")
	}
	var tags int
	s.db.QueryRow(`SELECT count(*) FROM challenge_tags WHERE challenge_id = 'docker-second'`).Scan(&tags)
	if tags != 0 {
		t.Fatalf("tags of a deleted challenge remain: %d", tags)
	}
	if p, _ := s.Progress("docker-second"); p.Status != model.LearnStatusInProgress {
		t.Fatalf("progress must survive a delete: %+v", p)
	}
}

func TestFailedMigrationBlocksLaterOnes(t *testing.T) {
	fsys := baseFS()
	fsys[file2] = migration("Broken", "  - { op: upsert, kind: challenge, id: docker-broken, data: { provider: docker } }\n")
	fsys[file3] = migration("Later", challengeYAML("docker-later", "docker", "beginner", "Later", "", ""))
	s := open(t, filepath.Join(t.TempDir(), FileName), fsys)
	st := s.Status()
	if st.Applied != 1 || st.Pending != 2 || len(st.Errors) != 1 || !strings.Contains(st.Errors[0], file2) {
		t.Fatalf("status = %+v", st)
	}
	if _, ok, _ := s.Challenge("docker-later"); ok {
		t.Fatal("a migration after a failed one was applied")
	}
	if _, ok, _ := s.Challenge("docker-first"); !ok {
		t.Fatal("content before the failure must be served")
	}
}

func TestDeleteOfMissingRollsBack(t *testing.T) {
	fsys := baseFS()
	fsys[file2] = migration("Mixed",
		challengeYAML("docker-new", "docker", "beginner", "New", "", ""),
		"  - { op: delete, kind: challenge, id: docker-nope }\n")
	s := open(t, filepath.Join(t.TempDir(), FileName), fsys)
	if st := s.Status(); len(st.Errors) != 1 || !strings.Contains(st.Errors[0], "does not exist") {
		t.Fatalf("errors = %v", st.Errors)
	}
	if _, ok, _ := s.Challenge("docker-new"); ok {
		t.Fatal("the failed migration's other operations must roll back")
	}
}

func TestUnusableDatabaseDoesNotPanic(t *testing.T) {
	// A directory where the file should be.
	dir := t.TempDir()
	s := OpenFile(dir, Options{Migrations: baseFS()})
	defer s.Close()
	if s.Err() == nil {
		t.Fatal("want an error for an unusable database")
	}
	if _, err := s.Providers(); err == nil {
		t.Fatal("readers must return the error")
	}
	if _, _, err := s.Challenge("docker-first"); err == nil {
		t.Fatal("readers must return the error")
	}
}

func TestFilters(t *testing.T) {
	fsys := baseFS()
	fsys[file2] = migration("More",
		challengeYAML("docker-cache", "docker", "advanced", "Cache aside", "cache, redis", "docker-first"),
		challengeYAML("docker-health", "docker", "intermediate", "Health checks", "reliability", ""))
	fsys["20260104000000_global_basics.yaml"] = migration("Global",
		challengeYAML("global-encrypt", "global", "beginner", "Encrypt data", "security", ""))
	s := open(t, filepath.Join(t.TempDir(), FileName), fsys)
	noErrors(t, s)
	cases := []struct {
		f    model.ChallengeFilter
		want []string
	}{
		{model.ChallengeFilter{}, []string{"docker-first", "docker-health", "docker-cache", "global-encrypt"}},
		{model.ChallengeFilter{Provider: "docker"}, []string{"docker-first", "docker-health", "docker-cache"}},
		{model.ChallengeFilter{Provider: "docker", Level: "advanced"}, []string{"docker-cache"}},
		{model.ChallengeFilter{Tag: "Redis"}, []string{"docker-cache"}},
		{model.ChallengeFilter{Search: "health"}, []string{"docker-health"}},
		{model.ChallengeFilter{Search: "ENCRYPT data"}, []string{"global-encrypt"}},
		{model.ChallengeFilter{Search: "cache nothing"}, []string{}},
		{model.ChallengeFilter{Provider: "aws"}, []string{}},
	}
	for _, c := range cases {
		list, err := s.Challenges(c.f)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(list); !slices.Equal(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.f, got, c.want)
		}
	}
}

func TestProgressTransitions(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), FileName), baseFS())
	if p, _ := s.Progress("docker-first"); p.Status != model.LearnStatusNotStarted {
		t.Fatalf("initial = %+v", p)
	}
	if _, err := s.MarkStarted("docker-first", "wf-1"); err != nil {
		t.Fatal(err)
	}
	fail := model.ChallengeResult{Goals: []model.ChallengeGoalResult{{ID: "a", Passed: true}, {ID: "b"}}}
	if err := s.RecordAttempt("docker-first", "wf-1", fail); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Progress("docker-first")
	if p.Status != model.LearnStatusInProgress || p.Attempts != 1 || !slices.Equal(p.CompletedGoals, []string{"a"}) {
		t.Fatalf("after a failing check: %+v", p)
	}
	pass := model.ChallengeResult{Passed: true, Goals: []model.ChallengeGoalResult{{ID: "a", Passed: true}, {ID: "b", Passed: true}}}
	if err := s.RecordAttempt("docker-first", "wf-1", pass); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Progress("docker-first")
	if p.Status != model.LearnStatusCompleted || p.Attempts != 2 || !slices.Equal(p.CompletedGoals, []string{"a", "b"}) || p.CompletedAt == nil {
		t.Fatalf("after a passing check: %+v", p)
	}
	// Starting again (a new workflow) keeps the completion.
	if p, _ := s.MarkStarted("docker-first", "wf-2"); p.Status != model.LearnStatusCompleted || p.WorkflowID != "wf-2" {
		t.Fatalf("restart: %+v", p)
	}
	if _, err := s.SetProgress("docker-first", model.LearnProgressUpdate{Status: model.LearnStatusCompleted}); err == nil {
		t.Fatal("completion must only come from a check")
	}
	p, err := s.SetProgress("docker-first", model.LearnProgressUpdate{Status: model.LearnStatusNotStarted})
	if err != nil || p.Status != model.LearnStatusNotStarted || p.Attempts != 0 || len(p.CompletedGoals) != 0 || p.CompletedAt != nil {
		t.Fatalf("reset: %+v %v", p, err)
	}
	providers, _ := s.Providers()
	for _, pr := range providers {
		if pr.Completed != 0 {
			t.Fatalf("completed after reset: %+v", pr)
		}
	}
}
