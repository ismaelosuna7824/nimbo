package store

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestSaveGetRoundTripAndPermissions(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wf := &model.Workflow{ID: NewID("Orders API"), Name: "Orders API", Nodes: []model.Node{{ID: "n1", Kind: model.KindResource, Type: "aws_sqs_queue", Name: "q"}}}
	if err := s.Save(wf); err != nil {
		t.Fatal(err)
	}
	if wf.CreatedAt.IsZero() || wf.UpdatedAt.IsZero() {
		t.Fatal("timestamps not set")
	}
	got, err := s.Get(wf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Orders API" || len(got.Nodes) != 1 || got.Nodes[0].Type != "aws_sqs_queue" {
		t.Fatalf("unexpected workflow: %+v", got)
	}
	path := filepath.Join(s.dir, wf.ID, workflowFile)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", di.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestListSortedByUpdatedAtDesc(t *testing.T) {
	s, _ := Open(t.TempDir())
	var ids []string
	for _, name := range []string{"first", "second", "third"} {
		wf := &model.Workflow{ID: NewID(name), Name: name}
		if err := s.Save(wf); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, wf.ID)
		time.Sleep(5 * time.Millisecond)
	}
	// Touch the first one again: it becomes the most recent.
	wf, _ := s.Get(ids[0])
	if err := s.Save(wf); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "broken-aaaaaa"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.dir, "broken-aaaaaa", workflowFile), []byte("{"), 0o600)

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ids[0], ids[2], ids[1]}
	if len(list) != len(want) {
		t.Fatalf("got %d summaries, want %d", len(list), len(want))
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("list[%d] = %s, want %s", i, list[i].ID, id)
		}
	}
}

// List caches summaries; a same-size rewrite by the store and an in-place
// edit outside the app must both show up on the next List.
func TestListReflectsChanges(t *testing.T) {
	s, _ := Open(t.TempDir())
	wf := &model.Workflow{ID: NewID("a"), Name: "aaaa"}
	if err := s.Save(wf); err != nil {
		t.Fatal(err)
	}
	name := func() string {
		t.Helper()
		list, err := s.List()
		if err != nil || len(list) != 1 {
			t.Fatalf("List = %v, %v", list, err)
		}
		return list[0].Name
	}
	if got := name(); got != "aaaa" {
		t.Fatalf("name = %q", got)
	}
	wf.Name = "bbbb"
	if err := s.write(wf); err != nil { // same size, timestamps untouched
		t.Fatal(err)
	}
	if got := name(); got != "bbbb" {
		t.Fatalf("after save, name = %q", got)
	}
	path := filepath.Join(s.dir, wf.ID, workflowFile)
	data, _ := os.ReadFile(path)
	edited := strings.Replace(string(data), `"bbbb"`, `"cccccc"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "cccccc" {
		t.Fatalf("after outside edit, name = %q", got)
	}
}

func TestDeleteRemovesAssetsAndReportsNotFound(t *testing.T) {
	s, _ := Open(t.TempDir())
	wf := &model.Workflow{ID: NewID("x"), Name: "x"}
	if err := s.Save(wf); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(s.AssetsDir(wf.ID), 0o700)
	os.WriteFile(filepath.Join(s.AssetsDir(wf.ID), "index.js"), []byte("x"), 0o600)
	if err := s.Delete(wf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, wf.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workflow dir still exists: %v", err)
	}
	if _, err := s.Get(wf.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
	if err := s.Delete(wf.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestGetRejectsPathTraversal(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, id := range []string{"../etc", "a/b", "", "UPPER"} {
		if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestNewID(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9-]+-[a-z2-7]{6}$`)
	cases := map[string]string{"Orders API!": "orders-api-", "": "workflow-", "  --  ": "workflow-"}
	for name, prefix := range cases {
		id := NewID(name)
		if !re.MatchString(id) || id[:len(prefix)] != prefix {
			t.Fatalf("NewID(%q) = %q", name, id)
		}
	}
	if NewID("a") == NewID("a") {
		t.Fatal("ids are not random")
	}
}

func TestAssignMissingIcons(t *testing.T) {
	s, _ := Open(t.TempDir())
	mascot := &model.WorkflowIcon{Glyph: model.MascotGlyph, Color: "violet"}
	chosen := &model.WorkflowIcon{Glyph: "cat", Color: "lime"}
	var ids []string
	for _, icon := range []*model.WorkflowIcon{nil, mascot, nil, chosen, nil} {
		wf := &model.Workflow{ID: NewID("wf"), Name: "wf", Icon: icon}
		if err := s.Save(wf); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, wf.ID)
		time.Sleep(2 * time.Millisecond)
	}
	before := map[string]time.Time{}
	for _, id := range ids {
		wf, _ := s.Get(id)
		before[id] = wf.UpdatedAt
	}

	n, err := s.AssignMissingIcons()
	if err != nil || n != 3 {
		t.Fatalf("AssignMissingIcons = %d, %v; want 3", n, err)
	}
	for i, id := range ids {
		wf, _ := s.Get(id)
		if !wf.UpdatedAt.Equal(before[id]) {
			t.Fatalf("%s UpdatedAt changed: %v -> %v", id, before[id], wf.UpdatedAt)
		}
		if wf.Icon == nil || wf.Icon.Validate() != nil {
			t.Fatalf("%s icon = %+v", id, wf.Icon)
		}
		switch i {
		case 1:
			if *wf.Icon != *mascot {
				t.Fatalf("mascot icon replaced: %+v", wf.Icon)
			}
		case 3:
			if *wf.Icon != *chosen {
				t.Fatalf("chosen icon replaced: %+v", wf.Icon)
			}
		}
	}
	list, _ := s.List()
	for i := 1; i < len(list); i++ {
		if list[i].Icon.Glyph == list[i-1].Icon.Glyph {
			t.Fatalf("neighbours share glyph %q", list[i].Icon.Glyph)
		}
	}
	if n, err := s.AssignMissingIcons(); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v; want 0", n, err)
	}
}
