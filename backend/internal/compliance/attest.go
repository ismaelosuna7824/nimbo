package compliance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Attestation is the user's word that a manual control is done.
type Attestation struct {
	Note string    `json:"note"`
	By   string    `json:"by,omitempty"`
	At   time.Time `json:"at"`
}

// Attestations of one workflow by control key (Key).
type Attestations map[string]Attestation

// Key identifies a control across frameworks: "<framework>/<control>".
func Key(frameworkID, controlID string) string { return frameworkID + "/" + controlID }

// ErrInvalidID is returned for workflow IDs that can't name a directory.
var ErrInvalidID = errors.New("invalid workflow id")

// The attestations of a workflow live in <home>/workflows/<id>/compliance.json,
// next to the workflow, so deleting the workflow deletes them.

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// attestMu serializes read-modify-write of the attestation files.
var attestMu sync.Mutex

func attestPath(home, wfID string) (string, error) {
	if !safeID.MatchString(wfID) {
		return "", ErrInvalidID
	}
	return filepath.Join(home, "workflows", wfID, "compliance.json"), nil
}

// LoadAttestations reads the attestations of workflow wfID (empty when it
// has none).
func LoadAttestations(home, wfID string) (Attestations, error) {
	attestMu.Lock()
	defer attestMu.Unlock()
	return load(home, wfID)
}

func load(home, wfID string) (Attestations, error) {
	path, err := attestPath(home, wfID)
	if err != nil {
		return nil, err
	}
	out := Attestations{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

// Attest stores a (replacing any earlier one for the same control).
func Attest(home, wfID, key string, a Attestation) error {
	a.Note, a.By = strings.TrimSpace(a.Note), strings.TrimSpace(a.By)
	return update(home, wfID, func(att Attestations) { att[key] = a })
}

// Unattest removes the attestation of a control (no error when there is
// none).
func Unattest(home, wfID, key string) error {
	return update(home, wfID, func(att Attestations) { delete(att, key) })
}

func update(home, wfID string, fn func(Attestations)) error {
	attestMu.Lock()
	defer attestMu.Unlock()
	att, err := load(home, wfID)
	if err != nil {
		return err
	}
	fn(att)
	path, _ := attestPath(home, wfID)
	data, err := json.MarshalIndent(att, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
