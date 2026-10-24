package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/learn"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Learn: roadmaps and challenges from the learn database (internal/learn)
// and the user's progress. Starting a challenge and grading it are in
// challenge.go.

// learnStore returns the learn database or 503 when it is not configured.
func (s *Server) learnStore() (*learn.Store, error) {
	if s.cfg.Learn == nil {
		return nil, errorf(http.StatusServiceUnavailable, "the learn database is not available")
	}
	return s.cfg.Learn, nil
}

// learnError maps an unusable database to 503.
func learnError(err error) error {
	if errors.Is(err, learn.ErrUnavailable) {
		return errorf(http.StatusServiceUnavailable, "%v", err)
	}
	return err
}

// learnOverview is GET /learn: the providers with their counts and the
// content migrations' status. A broken database still answers (empty
// providers, the error in status) so the Learn page can show it.
func (s *Server) learnOverview(w http.ResponseWriter, r *http.Request) error {
	st, err := s.learnStore()
	if err != nil {
		return err
	}
	out := model.LearnOverview{Providers: []model.LearnProvider{}, Status: st.Status()}
	providers, err := st.Providers()
	if err != nil && !errors.Is(err, learn.ErrUnavailable) {
		return err
	}
	if err == nil {
		out.Providers = providers
	}
	return ok(w, out)
}

func validLearnProvider(p string) error {
	if !slices.Contains(model.LearnProviders, p) {
		return errorf(http.StatusBadRequest, "unknown provider %q (one of %s)", p, strings.Join(model.LearnProviders, ", "))
	}
	return nil
}

// learnRoadmap is GET /learn/providers/{provider}/roadmap.
func (s *Server) learnRoadmap(w http.ResponseWriter, r *http.Request) error {
	st, err := s.learnStore()
	if err != nil {
		return err
	}
	provider := r.PathValue("provider")
	if err := validLearnProvider(provider); err != nil {
		return err
	}
	rm, found, err := st.Roadmap(provider)
	if err != nil {
		return learnError(err)
	}
	if !found {
		return errorf(http.StatusNotFound, "%s has no roadmap yet", learn.ProviderTitle(provider))
	}
	return ok(w, rm)
}

// learnChallenges is GET /learn/challenges?provider=&level=&tag=&q=.
func (s *Server) learnChallenges(w http.ResponseWriter, r *http.Request) error {
	st, err := s.learnStore()
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := model.ChallengeFilter{Provider: q.Get("provider"), Level: q.Get("level"), Tag: q.Get("tag"), Search: q.Get("q")}
	if f.Provider != "" {
		if err := validLearnProvider(f.Provider); err != nil {
			return err
		}
	}
	if f.Level != "" && !slices.Contains(model.LearnLevels, f.Level) {
		return errorf(http.StatusBadRequest, "unknown level %q (one of %s)", f.Level, strings.Join(model.LearnLevels, ", "))
	}
	list, err := st.Challenges(f)
	if err != nil {
		return learnError(err)
	}
	return ok(w, list)
}

// learnChallenge is GET /learn/challenges/{id}.
func (s *Server) learnChallenge(w http.ResponseWriter, r *http.Request) error {
	st, err := s.learnStore()
	if err != nil {
		return err
	}
	d, found, err := st.Detail(r.PathValue("id"))
	if err != nil {
		return learnError(err)
	}
	if !found {
		return errorf(http.StatusNotFound, "challenge %q not found", r.PathValue("id"))
	}
	return ok(w, d)
}

// learnProgress is GET /learn/progress: every challenge the user touched.
func (s *Server) learnProgress(w http.ResponseWriter, r *http.Request) error {
	st, err := s.learnStore()
	if err != nil {
		return err
	}
	list, err := st.AllProgress()
	if err != nil {
		return learnError(err)
	}
	return ok(w, list)
}

// putLearnProgress is PUT /learn/progress/{id} {status}: "not_started"
// resets the challenge, "in_progress" marks it started.
func (s *Server) putLearnProgress(w http.ResponseWriter, r *http.Request) error {
	st, err := s.learnStore()
	if err != nil {
		return err
	}
	id := r.PathValue("id")
	var u model.LearnProgressUpdate
	if err := decode(w, r, maxBody, &u); err != nil {
		return err
	}
	if u.Status != model.LearnStatusNotStarted && u.Status != model.LearnStatusInProgress {
		return errorf(http.StatusBadRequest, "status must be %q or %q (completion is recorded by a passing check)", model.LearnStatusNotStarted, model.LearnStatusInProgress)
	}
	if _, found, err := st.Challenge(id); err != nil {
		return learnError(err)
	} else if !found {
		return errorf(http.StatusNotFound, "challenge %q not found", id)
	}
	p, err := st.SetProgress(id, u)
	if err != nil {
		return learnError(err)
	}
	return ok(w, p)
}
