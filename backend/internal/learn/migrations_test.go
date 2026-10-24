package learn_test

import (
	"path/filepath"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/learn"
)

// TestMigrations validates every shipped content migration (file names,
// fields, ids, official https sources, check shapes and starters through
// the grading engine's challenge.Validate, references between content)
// and applies them all to a fresh database. Content authors run:
//
//	cd backend && go test ./internal/learn/ -run TestMigrations
func TestMigrations(t *testing.T) {
	for _, err := range learn.Lint(learn.Migrations(), challenge.Validate) {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	s := learn.OpenFile(filepath.Join(t.TempDir(), learn.FileName), learn.Options{ValidateChallenge: challenge.Validate})
	defer s.Close()
	st := s.Status()
	for _, e := range st.Errors {
		t.Error(e)
	}
	if st.Pending != 0 {
		t.Errorf("%d migrations not applied", st.Pending)
	}
	t.Logf("%d migrations applied", st.Applied)
}
