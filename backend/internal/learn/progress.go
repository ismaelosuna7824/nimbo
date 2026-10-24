package learn

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// progressOr returns the progress of id, or a not-started one.
func progressOr(all map[string]model.ChallengeProgress, id string) model.ChallengeProgress {
	if p, ok := all[id]; ok {
		return p
	}
	return model.ChallengeProgress{ChallengeID: id, Status: model.LearnStatusNotStarted, CompletedGoals: []string{}}
}

const progressColumns = `challenge_id, status, attempts, completed_goals, workflow_id, started_at, updated_at, completed_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanProgress(r rowScanner) (model.ChallengeProgress, error) {
	var p model.ChallengeProgress
	var goals string
	var started, updated, completed sql.NullString
	if err := r.Scan(&p.ChallengeID, &p.Status, &p.Attempts, &goals, &p.WorkflowID, &started, &updated, &completed); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(goals), &p.CompletedGoals); err != nil || p.CompletedGoals == nil {
		p.CompletedGoals = []string{}
	}
	p.StartedAt, p.UpdatedAt, p.CompletedAt = parseTime(started), parseTime(updated), parseTime(completed)
	return p, nil
}

func (s *Store) allProgress() (map[string]model.ChallengeProgress, error) {
	rows, err := s.db.Query(`SELECT ` + progressColumns + ` FROM progress`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]model.ChallengeProgress{}
	for rows.Next() {
		p, err := scanProgress(rows)
		if err != nil {
			return nil, err
		}
		out[p.ChallengeID] = p
	}
	return out, rows.Err()
}

// Progress returns the user's progress on a challenge (not started when
// there is none). It does not require the challenge to exist.
func (s *Store) Progress(id string) (model.ChallengeProgress, error) {
	if err := s.ready(); err != nil {
		return model.ChallengeProgress{}, err
	}
	return s.progress(s.db, id)
}

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func (s *Store) progress(q querier, id string) (model.ChallengeProgress, error) {
	p, err := scanProgress(q.QueryRow(`SELECT `+progressColumns+` FROM progress WHERE challenge_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return progressOr(nil, id), nil
	}
	return p, err
}

// AllProgress returns the progress of every challenge the user touched.
func (s *Store) AllProgress() ([]model.ChallengeProgress, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	all, err := s.allProgress()
	if err != nil {
		return nil, err
	}
	out := make([]model.ChallengeProgress, 0, len(all))
	for _, p := range all {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b model.ChallengeProgress) int {
		switch {
		case a.ChallengeID < b.ChallengeID:
			return -1
		case a.ChallengeID > b.ChallengeID:
			return 1
		}
		return 0
	})
	return out, nil
}

func (s *Store) save(tx *sql.Tx, p model.ChallengeProgress) error {
	goals, err := json.Marshal(nonNil(p.CompletedGoals))
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO progress (`+progressColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(challenge_id) DO UPDATE SET status = excluded.status, attempts = excluded.attempts,
	completed_goals = excluded.completed_goals, workflow_id = excluded.workflow_id, started_at = excluded.started_at,
	updated_at = excluded.updated_at, completed_at = excluded.completed_at`,
		p.ChallengeID, p.Status, p.Attempts, string(goals), p.WorkflowID,
		nullTime(p.StartedAt), nullTime(p.UpdatedAt), nullTime(p.CompletedAt))
	return err
}

// nullTime stores an optional time as RFC 3339 text or NULL.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// update reads, changes and writes a challenge's progress atomically.
func (s *Store) update(id string, change func(*model.ChallengeProgress) error) (model.ChallengeProgress, error) {
	if err := s.ready(); err != nil {
		return model.ChallengeProgress{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return model.ChallengeProgress{}, err
	}
	defer tx.Rollback()
	p, err := s.progress(tx, id)
	if err != nil {
		return p, err
	}
	if err := change(&p); err != nil {
		return p, err
	}
	now := s.now()
	p.UpdatedAt = &now
	if err := s.save(tx, p); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

// MarkStarted records that a workflow was created for the challenge: the
// challenge becomes in progress (a completed one stays completed).
func (s *Store) MarkStarted(challengeID, workflowID string) (model.ChallengeProgress, error) {
	return s.update(challengeID, func(p *model.ChallengeProgress) error {
		now := s.now()
		if p.StartedAt == nil {
			p.StartedAt = &now
		}
		if p.Status != model.LearnStatusCompleted {
			p.Status = model.LearnStatusInProgress
		}
		p.WorkflowID = workflowID
		return nil
	})
}

// RecordAttempt records a finished "Check my solution": one more attempt,
// the passed checks join the completed goals and a passing result
// completes the challenge (the first completion time is kept).
func (s *Store) RecordAttempt(challengeID, workflowID string, res model.ChallengeResult) error {
	_, err := s.update(challengeID, func(p *model.ChallengeProgress) error {
		now := s.now()
		p.Attempts++
		if workflowID != "" {
			p.WorkflowID = workflowID
		}
		if p.StartedAt == nil {
			p.StartedAt = &now
		}
		for _, g := range res.Goals {
			if g.Passed && !slices.Contains(p.CompletedGoals, g.ID) {
				p.CompletedGoals = append(p.CompletedGoals, g.ID)
			}
		}
		switch {
		case res.Passed:
			p.Status = model.LearnStatusCompleted
			if p.CompletedAt == nil {
				p.CompletedAt = &now
			}
		case p.Status != model.LearnStatusCompleted:
			p.Status = model.LearnStatusInProgress
		}
		return nil
	})
	return err
}

// SetProgress applies PUT /learn/progress/{id}: "not_started" resets the
// progress (attempts, goals, completion), "in_progress" marks the
// challenge started. Completion only comes from a passing check.
func (s *Store) SetProgress(challengeID string, u model.LearnProgressUpdate) (model.ChallengeProgress, error) {
	switch u.Status {
	case model.LearnStatusNotStarted:
		return s.update(challengeID, func(p *model.ChallengeProgress) error {
			*p = progressOr(nil, challengeID)
			return nil
		})
	case model.LearnStatusInProgress:
		return s.update(challengeID, func(p *model.ChallengeProgress) error {
			now := s.now()
			if p.StartedAt == nil {
				p.StartedAt = &now
			}
			if p.Status == model.LearnStatusNotStarted {
				p.Status = model.LearnStatusInProgress
			}
			return nil
		})
	}
	return model.ChallengeProgress{}, fmt.Errorf("status must be %q or %q (completion is recorded by a passing check)", model.LearnStatusNotStarted, model.LearnStatusInProgress)
}
