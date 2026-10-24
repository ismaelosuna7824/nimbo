package learn

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestLintErrors(t *testing.T) {
	valid := challengeYAML("docker-first", "docker", "beginner", "First", "containers", "")
	cases := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"bad file name", fstest.MapFS{"2026_docker_x.yaml": migration("x", valid)}, "YYYYMMDDHHMMSS"},
		{"bad timestamp", fstest.MapFS{"20261399000000_docker_x.yaml": migration("x", valid)}, "not a valid UTC date-time"},
		{"unknown provider", fstest.MapFS{"20260101000000_heroku_x.yaml": migration("x", valid)}, `provider "heroku"`},
		{"no description", fstest.MapFS{file1: {Data: []byte("operations:\n" + valid)}}, "description is required"},
		{"unknown top-level key", fstest.MapFS{file1: {Data: []byte("description: x\nextra: 1\noperations:\n" + valid)}}, `unknown field "extra"`},
		{"unknown data key", fstest.MapFS{file1: migration("x", strings.Replace(valid, "minutes: 15", "minutes: 15\n      duration: 3", 1))}, `unknown field "duration"`},
		{"bad op", fstest.MapFS{file1: migration("x", strings.Replace(valid, "op: upsert", "op: insert", 1))}, "op must be"},
		{"id of another provider", fstest.MapFS{file1: migration("x", challengeYAML("aws-first", "aws", "beginner", "First", "", ""))}, `must start with "docker-"`},
		{"provider mismatch", fstest.MapFS{file1: migration("x", strings.Replace(valid, "provider: docker", "provider: aws", 1))}, "does not match the id prefix"},
		{"bad level", fstest.MapFS{file1: migration("x", challengeYAML("docker-first", "docker", "guru", "First", "", ""))}, "level must be one of"},
		{"bad runtime", fstest.MapFS{file1: migration("x", strings.Replace(valid, "runtime: local", "runtime: cloud", 1))}, "runtime must be one of"},
		{"no checks", fstest.MapFS{file1: migration("x", strings.Replace(valid, "        - { id: valid, label: The project is valid, type: validate }\n", "", 1))}, "checks: at least one"},
		{"duplicate check id", fstest.MapFS{file1: migration("x", strings.Replace(valid, "        - { id: valid, label: The project is valid, type: validate }\n",
			"        - { id: valid, label: A, type: validate }\n        - { id: valid, label: B, type: deploy }\n", 1))}, `id "valid" is used twice`},
		{"http source", fstest.MapFS{file1: migration("x", strings.Replace(valid, "https://docs.docker.com/reference/compose-file/", "http://docs.docker.com/", 1))}, "absolute https URL"},
		{"community source", fstest.MapFS{file1: migration("x", strings.Replace(valid, "https://docs.docker.com/reference/compose-file/", "https://roadmap.sh/docker", 1))}, "not an official documentation site"},
		{"escaping solution path", fstest.MapFS{file1: migration("x", strings.Replace(valid, "solution: Do it.", "solution: Do it.\n      solutionFiles: { \"../x\": content }", 1))}, "relative path inside"},
		{"adaptations outside global", fstest.MapFS{file1: migration("x", strings.Replace(valid, "adaptations: {}", "adaptations: { aws: x }", 1))}, "only for global"},
		{"global without adaptations", fstest.MapFS{"20260101000000_global_x.yaml": migration("x", strings.Replace(challengeYAML("global-x", "global", "beginner", "X", "", ""), "adaptations: { aws: Use Amazon S3. }", "adaptations: {}", 1))}, "global challenges explain"},
		{"starter with both forms", fstest.MapFS{file1: migration("x", strings.Replace(valid, "{ templateId: docker-nginx-static }", "{ templateId: docker-nginx-static, kind: docker }", 1))}, "either templateId or kind"},
		{"escaping solution path", fstest.MapFS{file1: migration("x", strings.Replace(valid, "solution: Do it.", "solution: Do it.\n      solutionFiles: { \"../x\": y }", 1))}, "relative path inside"},
		{"duplicate id in a file", fstest.MapFS{file1: migration("x", valid, valid)}, "appears more than once"},
		{"roadmap id", fstest.MapFS{file1: migration("x", valid, strings.Replace(roadmapYAML, "id: docker-roadmap", "id: docker-path", 1))}, `roadmap id must be "docker-roadmap"`},
		{"roadmap levels out of order", fstest.MapFS{file1: migration("x", valid, strings.Replace(roadmapYAML, "        - level: beginner", "        - level: expert\n          title: E\n          goals: G\n          steps:\n            - { title: S, description: D, docs: [{ title: T, url: \"https://docs.docker.com/\" }] }\n        - level: beginner", 1))}, "out of order"},
		{"roadmap step without docs", fstest.MapFS{file1: migration("x", valid, strings.Replace(roadmapYAML, "              docs:\n                - { title: Get started, url: \"https://docs.docker.com/get-started/\" }\n", "", 1))}, "docs: at least one official source"},
		{"roadmap to a missing challenge", fstest.MapFS{file1: migration("x", roadmapYAML)}, "challenge docker-first does not exist"},
		{"missing prerequisite", fstest.MapFS{file1: migration("x", challengeYAML("docker-first", "docker", "beginner", "First", "", "docker-zero"))}, "prerequisite docker-zero does not exist"},
		{"prerequisite cycle", fstest.MapFS{file1: migration("x",
			challengeYAML("docker-a", "docker", "beginner", "A", "", "docker-b"),
			challengeYAML("docker-b", "docker", "beginner", "B", "", "docker-a"))}, "prerequisite cycle"},
		{"delete of missing content", fstest.MapFS{file1: migration("x", "  - { op: delete, kind: challenge, id: docker-nope }\n")}, "does not exist"},
		{"delete with data", fstest.MapFS{file1: migration("x", "  - { op: delete, kind: challenge, id: docker-first, data: { title: x } }\n")}, "delete takes no data"},
		{"bad tag", fstest.MapFS{file1: migration("x", challengeYAML("docker-first", "docker", "beginner", "First", "Big Tag", ""))}, `tag "Big Tag"`},
		{"duplicate YAML key", fstest.MapFS{file1: {Data: []byte("description: x\ndescription: y\noperations:\n" + valid)}}, "YAML"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := Lint(c.fsys, nil)
			joined := errors.Join(errs...)
			if joined == nil || !strings.Contains(joined.Error(), c.want) {
				t.Fatalf("want an error containing %q, got %v", c.want, joined)
			}
		})
	}
}

func TestLintValid(t *testing.T) {
	fsys := baseFS()
	fsys[file2] = migration("Remove", "  - { op: delete, kind: roadmap, id: docker-roadmap }\n")
	if errs := Lint(fsys, nil); len(errs) > 0 {
		t.Fatal(errors.Join(errs...))
	}
}

func TestLintCallsExtraValidation(t *testing.T) {
	errs := Lint(baseFS(), func(c model.Challenge) error { return errors.New("engine says no to " + c.ID) })
	if joined := errors.Join(errs...); joined == nil || !strings.Contains(joined.Error(), "engine says no to docker-first") {
		t.Fatalf("got %v", joined)
	}
}
