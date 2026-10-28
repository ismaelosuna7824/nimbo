// Package learn stores Nimbo's Learn content (roadmaps and challenges) and
// the user's progress in <home>/learn.db, a SQLite database fed by the
// content migrations embedded from migrations/*.yaml.
package learn

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// FileName is the database file in the Nimbo home.
const FileName = "learn.db"

// ErrUnavailable wraps the reason the database could not be opened.
var ErrUnavailable = errors.New("learn database unavailable")

// Options configure Open.
type Options struct {
	// Migrations holds the content migrations (*.yaml at its root);
	// nil means the embedded ones.
	Migrations fs.FS
	// ValidateChallenge checks a challenge beyond the fields this package
	// knows (check parameters, starters); the daemon passes
	// challenge.Validate. Nil skips it.
	ValidateChallenge func(model.Challenge) error
	// Now stamps applied migrations and progress (tests); nil is time.Now.
	Now func() time.Time
}

// Store is the learn database. Open never returns nil: when the database
// cannot be opened or a migration fails, Status reports why and the
// readers serve what is available (or ErrUnavailable).
type Store struct {
	db   *sql.DB
	opts Options
	// openErr is set when the database itself is unusable.
	openErr error

	mu     sync.Mutex // serializes progress writes
	status model.LearnStatus
}

// Open opens <home>/learn.db and applies the missing migrations.
func Open(home string, opts Options) *Store {
	return OpenFile(filepath.Join(home, FileName), opts)
}

// OpenFile opens the database at path and applies the missing
// migrations.
func OpenFile(path string, opts Options) *Store {
	if opts.Migrations == nil {
		opts.Migrations = Migrations()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Store{opts: opts, status: model.LearnStatus{Errors: []string{}}}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err == nil {
		// One connection: SQLite serializes writers anyway and this keeps
		// transactions and pragmas on the same connection.
		db.SetMaxOpenConns(1)
		err = createSchema(db)
	}
	if err != nil {
		if db != nil {
			db.Close()
		}
		s.openErr = fmt.Errorf("%w: %v", ErrUnavailable, err)
		s.status.Errors = append(s.status.Errors, s.openErr.Error())
		return s
	}
	s.db = db
	s.status = s.migrate()
	return s
}

// Close closes the database.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Status reports the migrations applied and the load errors.
func (s *Store) Status() model.LearnStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Errors = slices.Clone(st.Errors)
	return st
}

// Err is the first load error, nil when every migration applied cleanly.
func (s *Store) Err() error {
	st := s.Status()
	if len(st.Errors) == 0 {
		return nil
	}
	return errors.New(strings.Join(st.Errors, "; "))
}

const schemaVersion = 1

// createSchema creates the tables (PRAGMA user_version tracks the schema
// of the database itself, separate from content migrations).
func createSchema(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("learn.db schema version %d is newer than this Nimbo (%d)", v, schemaVersion)
	}
	if v == schemaVersion {
		return nil
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS migrations (
	filename    TEXT PRIMARY KEY,
	checksum    TEXT NOT NULL,
	description TEXT NOT NULL,
	applied_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS challenges (
	id         TEXT PRIMARY KEY,
	provider   TEXT NOT NULL,
	level      TEXT NOT NULL,
	title      TEXT NOT NULL,
	search     TEXT NOT NULL,
	data       TEXT NOT NULL,
	migration  TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS challenges_provider_level ON challenges(provider, level);
CREATE TABLE IF NOT EXISTS challenge_tags (
	challenge_id TEXT NOT NULL REFERENCES challenges(id) ON DELETE CASCADE,
	tag          TEXT NOT NULL,
	PRIMARY KEY (challenge_id, tag)
);
CREATE INDEX IF NOT EXISTS challenge_tags_tag ON challenge_tags(tag);
CREATE TABLE IF NOT EXISTS roadmaps (
	id         TEXT PRIMARY KEY,
	provider   TEXT NOT NULL UNIQUE,
	data       TEXT NOT NULL,
	migration  TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
-- Progress has no foreign key: content migrations never touch it, so it
-- survives a challenge being updated, removed or re-added.
CREATE TABLE IF NOT EXISTS progress (
	challenge_id    TEXT PRIMARY KEY,
	status          TEXT NOT NULL,
	attempts        INTEGER NOT NULL DEFAULT 0,
	completed_goals TEXT NOT NULL DEFAULT '[]',
	workflow_id     TEXT NOT NULL DEFAULT '',
	started_at      TEXT,
	updated_at      TEXT,
	completed_at    TEXT
);
PRAGMA user_version = 1;`)
	return err
}

func (s *Store) ready() error {
	if s.db == nil {
		return s.openErr
	}
	return nil
}

func (s *Store) now() time.Time { return s.opts.Now().UTC() }

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		return nil
	}
	return &t
}

// Challenge returns a challenge by ID.
func (s *Store) Challenge(id string) (model.Challenge, bool, error) {
	var c model.Challenge
	if err := s.ready(); err != nil {
		return c, false, err
	}
	var data string
	err := s.db.QueryRow(`SELECT data FROM challenges WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return c, false, fmt.Errorf("challenge %s: %w", id, err)
	}
	return c, true, nil
}

// Challenges lists the challenges matching f, ordered by provider, level
// (easiest first), position in the provider's roadmap and title, each
// with the user's progress.
func (s *Store) Challenges(f model.ChallengeFilter) ([]model.ChallengeSummary, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	q := `SELECT data FROM challenges WHERE 1 = 1`
	var args []any
	if f.Provider != "" {
		q += ` AND provider = ?`
		args = append(args, f.Provider)
	}
	if f.Level != "" {
		q += ` AND level = ?`
		args = append(args, f.Level)
	}
	if f.Tag != "" {
		q += ` AND id IN (SELECT challenge_id FROM challenge_tags WHERE tag = ?)`
		args = append(args, strings.ToLower(f.Tag))
	}
	for _, word := range strings.Fields(strings.ToLower(f.Search)) {
		q += ` AND instr(search, ?) > 0`
		args = append(args, word)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var list []model.Challenge
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			rows.Close()
			return nil, err
		}
		var c model.Challenge
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	progress, err := s.allProgress()
	if err != nil {
		return nil, err
	}
	order, err := s.roadmapOrder()
	if err != nil {
		return nil, err
	}
	sortChallenges(list, order)
	out := make([]model.ChallengeSummary, 0, len(list))
	for _, c := range list {
		out = append(out, summarize(c, progressOr(progress, c.ID)))
	}
	return out, nil
}

// roadmapOrder is each challenge's position in its provider's roadmap
// (levels, then steps, in order).
func (s *Store) roadmapOrder() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT provider, data FROM roadmaps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order := map[string]int{}
	for rows.Next() {
		var provider, data string
		var r model.Roadmap
		if err := rows.Scan(&provider, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return nil, fmt.Errorf("roadmap %s: %w", provider, err)
		}
		n := 0
		for _, lvl := range r.Levels {
			for _, st := range lvl.Steps {
				for _, id := range st.Challenges {
					// Only the provider's own challenges: the global
					// roadmap's links don't order other providers.
					if _, seen := order[id]; !seen && strings.HasPrefix(id, provider+"-") {
						order[id] = n
						n++
					}
				}
			}
		}
	}
	return order, rows.Err()
}

// sortChallenges orders by provider, level (easiest first), position in
// the provider's roadmap (challenges it doesn't link come last), title.
func sortChallenges(list []model.Challenge, order map[string]int) {
	pos := func(id string) int {
		if n, ok := order[id]; ok {
			return n
		}
		return len(order)
	}
	slices.SortStableFunc(list, func(a, b model.Challenge) int {
		if d := slices.Index(model.LearnProviders, a.Provider) - slices.Index(model.LearnProviders, b.Provider); d != 0 {
			return d
		}
		if d := slices.Index(model.LearnLevels, a.Level) - slices.Index(model.LearnLevels, b.Level); d != 0 {
			return d
		}
		if d := pos(a.ID) - pos(b.ID); d != 0 {
			return d
		}
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})
}

func summarize(c model.Challenge, p model.ChallengeProgress) model.ChallengeSummary {
	return model.ChallengeSummary{
		ID: c.ID, Provider: c.Provider, Level: c.Level, Title: c.Title, Summary: c.Summary,
		Minutes: c.Minutes, Tags: nonNil(c.Tags), Runtime: c.Runtime, SolvableWithoutCode: c.SolvableWithoutCode,
		Prerequisites: nonNil(c.Prerequisites), Checks: len(c.Checks), Progress: p,
	}
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// Detail returns a challenge with the user's progress, its prerequisites
// and the roadmap steps that link to it.
func (s *Store) Detail(id string) (model.ChallengeDetail, bool, error) {
	var d model.ChallengeDetail
	c, ok, err := s.Challenge(id)
	if err != nil || !ok {
		return d, ok, err
	}
	progress, err := s.allProgress()
	if err != nil {
		return d, false, err
	}
	d.Challenge = c
	d.Progress = progressOr(progress, id)
	d.Prerequisites = []model.LearnPrerequisite{}
	for _, pid := range c.Prerequisites {
		p := model.LearnPrerequisite{ID: pid, Completed: progressOr(progress, pid).Status == model.LearnStatusCompleted}
		if pc, found, err := s.Challenge(pid); err != nil {
			return d, false, err
		} else if found {
			p.Title = pc.Title
		} else {
			p.Missing = true
		}
		d.Prerequisites = append(d.Prerequisites, p)
	}
	// Its provider's roadmap first, then the others (the global roadmap
	// links every provider's challenges), named by provider.
	d.Steps = []string{}
	for _, p := range slices.Concat([]string{c.Provider}, slices.DeleteFunc(slices.Clone(model.LearnProviders), func(p string) bool { return p == c.Provider })) {
		r, found, err := s.Roadmap(p)
		if err != nil {
			return d, false, err
		}
		if !found {
			continue
		}
		prefix := ""
		if p != c.Provider {
			prefix = ProviderTitle(p) + " roadmap · "
		}
		for _, lvl := range r.Levels {
			for _, st := range lvl.Steps {
				if slices.Contains(st.Challenges, id) {
					d.Steps = append(d.Steps, prefix+lvl.Level+": "+st.Title)
				}
			}
		}
	}
	return d, true, nil
}

// Roadmap returns a provider's roadmap.
func (s *Store) Roadmap(provider string) (model.Roadmap, bool, error) {
	var r model.Roadmap
	if err := s.ready(); err != nil {
		return r, false, err
	}
	var data string
	err := s.db.QueryRow(`SELECT data FROM roadmaps WHERE provider = ?`, provider).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return r, false, fmt.Errorf("roadmap %s: %w", provider, err)
	}
	return r, true, nil
}

// Providers lists every provider (model.LearnProviders order) with its
// challenge counts per level and the user's completions. SQLite counts:
// decoding and sorting every challenge just to count them made this the
// slowest Learn query.
func (s *Store) Providers() ([]model.LearnProvider, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	type key struct{ provider, level string }
	counts := map[key]model.LearnLevelCount{}
	rows, err := s.db.Query(`SELECT c.provider, c.level, COUNT(*), COUNT(p.challenge_id)
		FROM challenges c LEFT JOIN progress p ON p.challenge_id = c.id AND p.status = ?
		GROUP BY c.provider, c.level`, model.LearnStatusCompleted)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k key
		var lc model.LearnLevelCount
		if err := rows.Scan(&k.provider, &k.level, &lc.Challenges, &lc.Completed); err != nil {
			rows.Close()
			return nil, err
		}
		counts[k] = lc
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	roadmaps := map[string]bool{}
	rows, err = s.db.Query(`SELECT provider FROM roadmaps`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		roadmaps[p] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]model.LearnProvider, 0, len(model.LearnProviders))
	for _, id := range model.LearnProviders {
		p := model.LearnProvider{ID: id, Title: ProviderTitle(id), HasRoadmap: roadmaps[id], Levels: make([]model.LearnLevelCount, 0, len(model.LearnLevels))}
		for _, lvl := range model.LearnLevels {
			lc := counts[key{id, lvl}]
			lc.Level = lvl
			p.Challenges += lc.Challenges
			p.Completed += lc.Completed
			p.Levels = append(p.Levels, lc)
		}
		out = append(out, p)
	}
	return out, nil
}

// ProviderTitle is a provider's display name.
func ProviderTitle(id string) string {
	switch id {
	case model.LearnProviderAWS:
		return "AWS"
	case model.LearnProviderAzure:
		return "Azure"
	case model.LearnProviderGCP:
		return "Google Cloud"
	case model.LearnProviderOCI:
		return "Oracle Cloud"
	case model.LearnProviderDocker:
		return "Docker"
	case model.LearnProviderKubernetes:
		return "Kubernetes"
	case model.LearnProviderGlobal:
		return "Global"
	}
	return id
}
