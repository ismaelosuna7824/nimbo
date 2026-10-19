// Package settings persists the user's app preferences in
// <home>/settings.json.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// Modes accepted by Settings.DefaultMode.
const (
	ModeGuided = "guided"
	ModePro    = "pro"
)

const fileName = "settings.json"

// ErrInvalid wraps validation failures of Normalize.
var ErrInvalid = errors.New("invalid settings")

// Defaults returns the settings used when nothing is stored.
func Defaults() model.Settings {
	return model.Settings{
		DefaultMode:      ModeGuided,
		Docker:           sandbox.DefaultDocker,
		Images:           sandbox.DefaultImages(),
		TerraformImage:   sandbox.DefaultTerraformImage,
		DefaultAWSRegion: sandbox.DefaultAWSRegion,
		BestPractices:    model.BestPracticeSettings{DisabledRules: []string{}},
	}
}

var (
	// imageRef accepts [registry[:port]/]path[:tag][@digest] in lower case.
	imageRef  = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]+)?(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(?:@sha256:[a-f0-9]{64})?$`)
	awsRegion = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]$`)
	ruleCode  = regexp.MustCompile(`^bp_[a-z0-9_]+$`)
)

// Normalize returns s with empty fields set to their defaults, surrounding
// whitespace trimmed and every image map entry present, or an error wrapping
// ErrInvalid naming the first invalid field.
func Normalize(s model.Settings) (model.Settings, error) {
	d := Defaults()
	out := model.Settings{
		DefaultMode:      strings.TrimSpace(s.DefaultMode),
		Docker:           strings.TrimSpace(s.Docker),
		Images:           d.Images,
		TerraformImage:   strings.TrimSpace(s.TerraformImage),
		DefaultAWSRegion: strings.TrimSpace(s.DefaultAWSRegion),
	}
	if out.DefaultMode == "" {
		out.DefaultMode = d.DefaultMode
	}
	if out.DefaultMode != ModeGuided && out.DefaultMode != ModePro {
		return model.Settings{}, fmt.Errorf("%w: defaultMode must be %q or %q", ErrInvalid, ModeGuided, ModePro)
	}
	if out.Docker == "" {
		out.Docker = d.Docker
	}
	if strings.ContainsAny(out.Docker, "\x00\n\r") {
		return model.Settings{}, fmt.Errorf("%w: docker must be a program name or path", ErrInvalid)
	}
	if strings.ContainsRune(out.Docker, os.PathSeparator) && !filepath.IsAbs(out.Docker) {
		return model.Settings{}, fmt.Errorf("%w: docker path must be absolute (or a program name found on PATH)", ErrInvalid)
	}
	for c, img := range s.Images {
		if _, known := d.Images[c]; !known {
			return model.Settings{}, fmt.Errorf("%w: unknown cloud %q in images", ErrInvalid, c)
		}
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		if !imageRef.MatchString(img) {
			return model.Settings{}, fmt.Errorf("%w: %s image %q is not a valid image reference", ErrInvalid, c, img)
		}
		out.Images[c] = img
	}
	if out.TerraformImage == "" {
		out.TerraformImage = d.TerraformImage
	}
	if !imageRef.MatchString(out.TerraformImage) {
		return model.Settings{}, fmt.Errorf("%w: terraformImage %q is not a valid image reference", ErrInvalid, out.TerraformImage)
	}
	if out.DefaultAWSRegion == "" {
		out.DefaultAWSRegion = d.DefaultAWSRegion
	}
	if !awsRegion.MatchString(out.DefaultAWSRegion) {
		return model.Settings{}, fmt.Errorf("%w: defaultAWSRegion %q is not an AWS region (e.g. us-east-1)", ErrInvalid, out.DefaultAWSRegion)
	}
	out.BestPractices.Disabled = s.BestPractices.Disabled
	out.BestPractices.DisabledRules = []string{}
	for _, code := range s.BestPractices.DisabledRules {
		code = strings.TrimSpace(code)
		if !ruleCode.MatchString(code) {
			return model.Settings{}, fmt.Errorf("%w: best-practice rule %q is not a rule code (bp_*)", ErrInvalid, code)
		}
		if !slices.Contains(out.BestPractices.DisabledRules, code) {
			out.BestPractices.DisabledRules = append(out.BestPractices.DisabledRules, code)
		}
	}
	slices.Sort(out.BestPractices.DisabledRules)
	return out, nil
}

// Store reads and writes settings.json. It is safe for concurrent use.
type Store struct {
	path string

	mu  sync.Mutex
	cur model.Settings
}

// Open loads <home>/settings.json; a missing or unparsable file yields the
// defaults (an unparsable file is replaced by the next Put). Stored values
// that no longer validate fall back to their defaults field by field so a bad
// edit never prevents the daemon from starting.
func Open(home string) (*Store, error) {
	st := &Store{path: filepath.Join(home, fileName), cur: Defaults()}
	data, err := os.ReadFile(st.path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var s model.Settings
	if json.Unmarshal(data, &s) == nil {
		st.cur = lenient(s)
	}
	return st, nil
}

// lenient normalizes s, keeping the defaults for each field that does not
// validate.
func lenient(s model.Settings) model.Settings {
	cur := Defaults()
	try := func(apply func(*model.Settings)) {
		next := clone(cur)
		apply(&next)
		if n, err := Normalize(next); err == nil {
			cur = n
		}
	}
	try(func(p *model.Settings) { p.DefaultMode = s.DefaultMode })
	try(func(p *model.Settings) { p.Docker = s.Docker })
	try(func(p *model.Settings) { p.TerraformImage = s.TerraformImage })
	try(func(p *model.Settings) { p.DefaultAWSRegion = s.DefaultAWSRegion })
	try(func(p *model.Settings) { p.BestPractices = s.BestPractices })
	for c, img := range s.Images {
		try(func(p *model.Settings) { p.Images[c] = img })
	}
	return cur
}

// Get returns the current settings.
func (st *Store) Get() model.Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return clone(st.cur)
}

// Put validates s, writes it atomically (0600) and returns the stored value.
func (st *Store) Put(s model.Settings) (model.Settings, error) {
	n, err := Normalize(s)
	if err != nil {
		return model.Settings{}, err
	}
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return model.Settings{}, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := writeFileAtomic(st.path, append(data, '\n')); err != nil {
		return model.Settings{}, err
	}
	st.cur = n
	return clone(n), nil
}

func clone(s model.Settings) model.Settings {
	images := make(map[model.Cloud]string, len(s.Images))
	for c, img := range s.Images {
		images[c] = img
	}
	s.Images = images
	s.BestPractices.DisabledRules = slices.Clone(s.BestPractices.DisabledRules)
	return s
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
