package model

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

func TestRandomWorkflowIcon(t *testing.T) {
	for range 200 {
		icon := RandomWorkflowIcon()
		if !slices.Contains(WorkflowIconGlyphs, icon.Glyph) || !slices.Contains(WorkflowIconColors, icon.Color) || icon.Validate() != nil {
			t.Fatalf("random icon %+v", icon)
		}
	}
	// Avoided glyphs and the most recent color are skipped while others remain.
	last := &WorkflowIcon{Glyph: "cat", Color: "pink"}
	older := &WorkflowIcon{Glyph: "bolt", Color: "amber"}
	for range 200 {
		icon := RandomWorkflowIcon(last, nil, older)
		if icon.Glyph == "cat" || icon.Glyph == "bolt" || icon.Color == "pink" {
			t.Fatalf("icon %+v repeats an avoided one", icon)
		}
	}
	// Avoiding everything still yields an icon.
	all := make([]*WorkflowIcon, 0, len(WorkflowIconGlyphs))
	for _, g := range WorkflowIconGlyphs {
		all = append(all, &WorkflowIcon{Glyph: g, Color: "violet"})
	}
	if icon := RandomWorkflowIcon(all...); icon.Validate() != nil {
		t.Fatalf("icon %+v", icon)
	}
}

func TestWorkflowIconValidate(t *testing.T) {
	for _, ok := range []*WorkflowIcon{nil, {Glyph: MascotGlyph, Color: "violet"}, {Glyph: "rocket", Color: "pink"}} {
		if err := ok.Validate(); err != nil {
			t.Fatalf("%+v: %v", ok, err)
		}
	}
	for _, bad := range []*WorkflowIcon{{Glyph: "", Color: "pink"}, {Glyph: "Rocket", Color: "pink"}, {Glyph: "rocket", Color: "../x"}} {
		if bad.Validate() == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

// The random-pick lists mirror the desktop's glyph and color tables.
func TestWorkflowIconListsMatchDesktop(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "apps", "desktop", "src", "components", "workflow-icon")
	idRe := regexp.MustCompile(`(?m)^\s*(?:\{ )?id: "([a-z0-9-]+)"`)
	for file, want := range map[string][]string{"glyphs.ts": WorkflowIconGlyphs, "colors.ts": WorkflowIconColors} {
		src, err := os.ReadFile(filepath.Join(dir, file))
		if errors.Is(err, os.ErrNotExist) {
			t.Skip("desktop sources not checked out")
		}
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range idRe.FindAllStringSubmatch(string(src), -1) {
			got = append(got, m[1])
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s ids = %v, want %v", file, got, want)
		}
	}
}
