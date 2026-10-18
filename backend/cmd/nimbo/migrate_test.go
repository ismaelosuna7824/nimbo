package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyHome(t *testing.T) {
	user := t.TempDir()
	home := filepath.Join(user, ".nimbo")
	legacy := filepath.Join(user, ".iac-cloud")

	if ok, err := migrateLegacyHome(user, home); err != nil || ok {
		t.Fatalf("no legacy dir: got %v, %v", ok, err)
	}

	if err := os.MkdirAll(filepath.Join(legacy, "workflows", "wf1"), 0o700); err != nil {
		t.Fatal(err)
	}
	ok, err := migrateLegacyHome(user, home)
	if err != nil || !ok {
		t.Fatalf("migrate: got %v, %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(home, "workflows", "wf1")); err != nil {
		t.Fatalf("migrated data missing: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy dir still present: %v", err)
	}

	// Existing new home: legacy dir is left alone.
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if ok, err := migrateLegacyHome(user, home); err != nil || ok {
		t.Fatalf("existing home: got %v, %v", ok, err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("legacy dir must not be touched when home exists")
	}
}
