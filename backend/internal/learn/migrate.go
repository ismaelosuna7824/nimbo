package learn

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

//go:embed migrations/*.yaml
var embedded embed.FS

// Migrations is the embedded content migrations (*.yaml at the root).
func Migrations() fs.FS {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}

// Migration operations and kinds.
const (
	OpUpsert = "upsert"
	OpDelete = "delete"

	KindChallenge = "challenge"
	KindRoadmap   = "roadmap"
)

// migrationFile is the YAML document of a content migration.
type migrationFile struct {
	Description string      `json:"description"`
	Operations  []operation `json:"operations"`
}

type operation struct {
	Op   string          `json:"op"`
	Kind string          `json:"kind"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Migration is a parsed content migration.
type Migration struct {
	Filename    string
	Provider    string // from the file name
	Checksum    string
	Description string
	Ops         []Op
}

// Op is one decoded operation: Challenge or Roadmap is set for upserts.
type Op struct {
	Op        string
	Kind      string
	ID        string
	Challenge *model.Challenge
	Roadmap   *model.Roadmap
}

// checksum is the hex SHA-256 of a migration file.
func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// migrationNames lists the *.yaml files of fsys in apply order.
func migrationNames(fsys fs.FS) ([]string, error) {
	names, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}

// decodeStrict decodes JSON into v rejecting unknown fields and trailing
// data.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the document")
	}
	return nil
}

// yamlToJSON converts a YAML document (YAML 1.2: only true/false are
// booleans; duplicate keys are errors) to JSON, so the content types'
// json tags are the migration keys.
func yamlToJSON(data []byte) ([]byte, error) {
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("keys must be strings: %w", err)
	}
	return js, nil
}

// ParseMigration decodes and validates one migration file on its own
// (fields, ids, check shapes via validateChallenge); references to other
// content are checked on the whole state (see Lint).
func ParseMigration(filename string, data []byte, validateChallenge func(model.Challenge) error) (Migration, error) {
	m := Migration{Filename: filename, Checksum: checksum(data)}
	provider, err := parseFilename(filename)
	if err != nil {
		return m, err
	}
	m.Provider = provider
	js, err := yamlToJSON(data)
	if err != nil {
		return m, fmt.Errorf("YAML: %w", err)
	}
	var f migrationFile
	if err := decodeStrict(js, &f); err != nil {
		return m, fmt.Errorf("migration: %w", err)
	}
	m.Description = strings.TrimSpace(f.Description)
	var errs []error
	if m.Description == "" {
		errs = append(errs, errors.New("description is required"))
	}
	if len(f.Operations) == 0 {
		errs = append(errs, errors.New("operations: at least one is required"))
	}
	seen := map[string]bool{}
	for i, raw := range f.Operations {
		op, err := parseOp(raw, provider, validateChallenge)
		if err != nil {
			errs = append(errs, fmt.Errorf("operations[%d] (%s %s %s): %w", i, raw.Op, raw.Kind, raw.ID, err))
			continue
		}
		key := op.Kind + "/" + op.ID
		if seen[key] {
			errs = append(errs, fmt.Errorf("operations[%d]: %s %s appears more than once in this migration", i, op.Kind, op.ID))
		}
		seen[key] = true
		m.Ops = append(m.Ops, op)
	}
	return m, errors.Join(errs...)
}

func parseOp(raw operation, fileProvider string, validateChallenge func(model.Challenge) error) (Op, error) {
	op := Op{Op: raw.Op, Kind: raw.Kind, ID: raw.ID}
	switch raw.Op {
	case OpUpsert, OpDelete:
	default:
		return op, fmt.Errorf("op must be %q or %q", OpUpsert, OpDelete)
	}
	switch raw.Kind {
	case KindChallenge, KindRoadmap:
	default:
		return op, fmt.Errorf("kind must be %q or %q", KindChallenge, KindRoadmap)
	}
	if err := validateID(raw.Kind, raw.ID, fileProvider); err != nil {
		return op, err
	}
	hasData := len(raw.Data) > 0 && string(raw.Data) != "null"
	if raw.Op == OpDelete {
		if hasData {
			return op, errors.New("delete takes no data")
		}
		return op, nil
	}
	if !hasData {
		return op, errors.New("upsert needs data")
	}
	switch raw.Kind {
	case KindChallenge:
		var c model.Challenge
		if err := decodeStrict(raw.Data, &c); err != nil {
			return op, fmt.Errorf("data: %w", err)
		}
		if c.ID != "" && c.ID != raw.ID {
			return op, fmt.Errorf("data.id %q differs from the operation id", c.ID)
		}
		c.ID = raw.ID
		if err := checkChallenge(c, validateChallenge); err != nil {
			return op, err
		}
		op.Challenge = &c
	case KindRoadmap:
		var r model.Roadmap
		if err := decodeStrict(raw.Data, &r); err != nil {
			return op, fmt.Errorf("data: %w", err)
		}
		if r.ID != "" && r.ID != raw.ID {
			return op, fmt.Errorf("data.id %q differs from the operation id", r.ID)
		}
		r.ID = raw.ID
		if err := checkRoadmap(r); err != nil {
			return op, err
		}
		op.Roadmap = &r
	}
	return op, nil
}

// migrate applies the missing migrations in file name order, one
// transaction each, and reports what it could not apply. A migration that
// fails stops the ones after it (they may depend on it); an applied
// migration whose file changed is reported and left as applied.
func (s *Store) migrate() model.LearnStatus {
	st := model.LearnStatus{Errors: []string{}}
	fail := func(format string, args ...any) { st.Errors = append(st.Errors, fmt.Sprintf(format, args...)) }
	names, err := migrationNames(s.opts.Migrations)
	if err != nil {
		fail("listing migrations: %v", err)
		return st
	}
	applied, err := s.appliedMigrations()
	if err != nil {
		fail("reading applied migrations: %v", err)
		return st
	}
	blocked := false
	for _, name := range names {
		data, err := fs.ReadFile(s.opts.Migrations, name)
		if err != nil {
			fail("%s: %v", name, err)
			blocked = true
			st.Pending++
			continue
		}
		if sum, ok := applied[name]; ok {
			st.Applied++
			if sum != checksum(data) {
				fail("%s was edited after it was applied (checksum changed): never edit an applied migration, add a new one", name)
			}
			continue
		}
		if blocked {
			st.Pending++
			continue
		}
		m, err := ParseMigration(name, data, s.opts.ValidateChallenge)
		if err == nil {
			err = s.apply(m)
		}
		if err != nil {
			fail("%s: %v", name, err)
			blocked = true
			st.Pending++
			continue
		}
		st.Applied++
	}
	state, err := s.loadState()
	if err != nil {
		fail("reading content: %v", err)
		return st
	}
	for _, e := range state.references() {
		fail("%v", e)
	}
	return st
}

func (s *Store) appliedMigrations() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT filename, checksum FROM migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, sum string
		if err := rows.Scan(&name, &sum); err != nil {
			return nil, err
		}
		out[name] = sum
	}
	return out, rows.Err()
}

// apply runs a migration's operations and records it, atomically.
func (s *Store) apply(m Migration) (err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	now := formatTime(s.now())
	for _, op := range m.Ops {
		if err := applyOp(tx, m.Filename, now, op); err != nil {
			return fmt.Errorf("%s %s %s: %w", op.Op, op.Kind, op.ID, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO migrations (filename, checksum, description, applied_at) VALUES (?, ?, ?, ?)`,
		m.Filename, m.Checksum, m.Description, now); err != nil {
		return err
	}
	return tx.Commit()
}

func applyOp(tx *sql.Tx, migration, now string, op Op) error {
	switch {
	case op.Op == OpDelete:
		table := "challenges"
		if op.Kind == KindRoadmap {
			table = "roadmaps"
		}
		res, err := tx.Exec(`DELETE FROM `+table+` WHERE id = ?`, op.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.New("nothing to delete: it does not exist")
		}
		return nil
	case op.Challenge != nil:
		c := *op.Challenge
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO challenges (id, provider, level, title, search, data, migration, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET provider = excluded.provider, level = excluded.level, title = excluded.title,
	search = excluded.search, data = excluded.data, migration = excluded.migration, updated_at = excluded.updated_at`,
			c.ID, c.Provider, c.Level, c.Title, searchText(c), string(data), migration, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM challenge_tags WHERE challenge_id = ?`, c.ID); err != nil {
			return err
		}
		for _, tag := range c.Tags {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO challenge_tags (challenge_id, tag) VALUES (?, ?)`, c.ID, strings.ToLower(tag)); err != nil {
				return err
			}
		}
		return nil
	case op.Roadmap != nil:
		r := *op.Roadmap
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO roadmaps (id, provider, data, migration, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET provider = excluded.provider, data = excluded.data, migration = excluded.migration, updated_at = excluded.updated_at`,
			r.ID, r.Provider, string(data), migration, now)
		return err
	}
	return errors.New("upsert without data")
}

// searchText is what the catalog search matches: lower-case title,
// summary, problem and tags.
func searchText(c model.Challenge) string {
	return strings.ToLower(strings.Join([]string{c.ID, c.Title, c.Summary, c.Problem, strings.Join(c.Tags, " ")}, "\n"))
}

// state is the whole content (applied or simulated) for reference checks.
type state struct {
	challenges map[string]model.Challenge
	roadmaps   map[string]model.Roadmap
}

func newState() state {
	return state{challenges: map[string]model.Challenge{}, roadmaps: map[string]model.Roadmap{}}
}

// applyOp applies an operation to the in-memory state like the database
// does.
func (st state) applyOp(op Op) error {
	switch {
	case op.Op == OpDelete && op.Kind == KindChallenge:
		if _, ok := st.challenges[op.ID]; !ok {
			return errors.New("nothing to delete: it does not exist")
		}
		delete(st.challenges, op.ID)
	case op.Op == OpDelete:
		if _, ok := st.roadmaps[op.ID]; !ok {
			return errors.New("nothing to delete: it does not exist")
		}
		delete(st.roadmaps, op.ID)
	case op.Challenge != nil:
		st.challenges[op.ID] = *op.Challenge
	case op.Roadmap != nil:
		st.roadmaps[op.ID] = *op.Roadmap
	}
	return nil
}

func (s *Store) loadState() (state, error) {
	st := newState()
	rows, err := s.db.Query(`SELECT id, data FROM challenges`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var id, data string
		var c model.Challenge
		if err := rows.Scan(&id, &data); err == nil {
			err = json.Unmarshal([]byte(data), &c)
		}
		if err != nil {
			rows.Close()
			return st, err
		}
		st.challenges[id] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}
	rows, err = s.db.Query(`SELECT id, data FROM roadmaps`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, data string
		var r model.Roadmap
		if err := rows.Scan(&id, &data); err == nil {
			err = json.Unmarshal([]byte(data), &r)
		}
		if err != nil {
			return st, err
		}
		st.roadmaps[id] = r
	}
	return st, rows.Err()
}

// Lint validates every migration of fsys: each file on its own (all
// errors, not only the first file's), then the content after applying
// them all from scratch (deletes of missing content, references). It is
// what the migrations test and CI run.
func Lint(fsys fs.FS, validateChallenge func(model.Challenge) error) []error {
	names, err := migrationNames(fsys)
	if err != nil {
		return []error{err}
	}
	var errs []error
	st := newState()
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		m, err := ParseMigration(name, data, validateChallenge)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		for _, op := range m.Ops {
			if err := st.applyOp(op); err != nil {
				errs = append(errs, fmt.Errorf("%s: %s %s %s: %w", name, op.Op, op.Kind, op.ID, err))
			}
		}
	}
	return append(errs, st.references()...)
}
