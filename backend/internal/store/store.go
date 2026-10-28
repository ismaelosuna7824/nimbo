// Package store persists workflows under <home>/workflows/<id>/workflow.json.
package store

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ErrNotFound is returned when a workflow does not exist.
var ErrNotFound = errors.New("workflow not found")

const workflowFile = "workflow.json"

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Store reads and writes workflows on disk. It is safe for concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex

	// summaries caches List entries per workflow by workflow.json version,
	// so listing re-parses only workflows changed since the last List
	// instead of every full workflow (nodes, files, diagnostics) each time.
	summaryMu sync.Mutex
	summaries map[string]cachedSummary
}

// cachedSummary is a List entry and the workflow.json it was read from.
// write replaces the file (a new inode), so a save never matches even on
// file systems with coarse timestamps; in-place edits change mtime or size.
type cachedSummary struct {
	file os.FileInfo
	sum  model.WorkflowSummary
}

// Open creates <home>/workflows if needed and returns a store rooted there.
func Open(home string) (*Store, error) {
	dir := filepath.Join(home, "workflows")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create workflows dir: %w", err)
	}
	return &Store{dir: dir, summaries: map[string]cachedSummary{}}, nil
}

// List returns every readable workflow, most recently updated first.
// Directories without a parseable workflow.json are skipped.
func (s *Store) List() ([]model.WorkflowSummary, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	out := []model.WorkflowSummary{}
	for _, e := range entries {
		if !e.IsDir() || !validID.MatchString(e.Name()) {
			continue
		}
		sum, err := s.summary(e.Name())
		if err != nil {
			continue
		}
		out = append(out, sum)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// summary returns the List entry of workflow id, parsing workflow.json
// only when it is not the version cached.
func (s *Store) summary(id string) (model.WorkflowSummary, error) {
	f, err := os.Open(filepath.Join(s.dir, id, workflowFile))
	if err != nil {
		return model.WorkflowSummary{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return model.WorkflowSummary{}, err
	}
	s.summaryMu.Lock()
	c, hit := s.summaries[id]
	s.summaryMu.Unlock()
	if hit && os.SameFile(c.file, info) && c.file.Size() == info.Size() && c.file.ModTime().Equal(info.ModTime()) {
		return c.sum, nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return model.WorkflowSummary{}, err
	}
	wf, err := decode(id, data)
	if err != nil {
		return model.WorkflowSummary{}, err
	}
	sum := summarize(wf)
	s.summaryMu.Lock()
	s.summaries[id] = cachedSummary{file: info, sum: sum}
	s.summaryMu.Unlock()
	return sum, nil
}

func summarize(wf *model.Workflow) model.WorkflowSummary {
	clouds := wf.Clouds
	if clouds == nil {
		clouds = []model.Cloud{}
	}
	count := len(wf.Nodes)
	if wf.IsDocker() && wf.Compose != nil {
		count = 0
		for _, n := range wf.Compose.Graph.Nodes {
			if n.Kind == model.ComposeService {
				count++
			}
		}
	}
	if wf.IsKubernetes() && wf.Kubernetes != nil {
		count = len(wf.Kubernetes.Graph.Nodes)
	}
	return model.WorkflowSummary{
		ID:          wf.ID,
		Name:        wf.Name,
		Description: wf.Description,
		Kind:        wf.Kind,
		UpdatedAt:   wf.UpdatedAt,
		NodeCount:   count,
		Clouds:      clouds,
		UsedClouds:  wf.UsedClouds,
		Archived:    wf.Archived,
		Icon:        wf.Icon,
	}
}

// Get loads one workflow.
func (s *Store) Get(id string) (*model.Workflow, error) {
	if !validID.MatchString(id) {
		return nil, ErrNotFound
	}
	return s.read(id)
}

func (s *Store) read(id string) (*model.Workflow, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, id, workflowFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(id, data)
}

func decode(id string, data []byte) (*model.Workflow, error) {
	var wf model.Workflow
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse workflow %s: %w", id, err)
	}
	wf.ID = id
	// Derived, so files saved before the field existed get it too.
	wf.UsedClouds = wf.ResourceClouds()
	return &wf, nil
}

// Save writes wf atomically (temp file + rename) with mode 0600. It sets
// UpdatedAt to now and CreatedAt when it is zero.
func (s *Store) Save(wf *model.Workflow) error {
	if !validID.MatchString(wf.ID) {
		return fmt.Errorf("invalid workflow id %q", wf.ID)
	}
	now := time.Now().UTC()
	if wf.CreatedAt.IsZero() {
		wf.CreatedAt = now
	}
	wf.UpdatedAt = now
	return s.write(wf)
}

// RecentIcons returns the icons of up to n workflows, most recently updated
// first, for picking a new random icon that stands out from them.
func (s *Store) RecentIcons(n int) ([]*model.WorkflowIcon, error) {
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	var out []*model.WorkflowIcon
	for _, w := range list[:min(n, len(list))] {
		out = append(out, w.Icon)
	}
	return out, nil
}

// AssignMissingIcons gives every workflow without an icon (saved before icons
// existed) a random one, keeping UpdatedAt so recents keep their order.
// Neighbours in recents order get different glyphs. It returns how many
// workflows changed; with none missing it writes nothing.
func (s *Store) AssignMissingIcons() (int, error) {
	list, err := s.List()
	if err != nil {
		return 0, err
	}
	var recent []*model.WorkflowIcon // most recent first
	changed := 0
	for i, sum := range list {
		if sum.Icon == nil {
			wf, err := s.read(sum.ID)
			if err != nil {
				return changed, err
			}
			// Avoid the newer neighbours (already assigned) and the older
			// one when it already has an icon: list is walked newest first,
			// so without it the new glyph could repeat the next card's.
			avoid := recent
			if i+1 < len(list) && list[i+1].Icon != nil {
				avoid = append(slices.Clone(recent), list[i+1].Icon)
			}
			wf.Icon = model.RandomWorkflowIcon(avoid...)
			if err := s.write(wf); err != nil {
				return changed, err
			}
			sum.Icon = wf.Icon
			changed++
		}
		recent = append([]*model.WorkflowIcon{sum.Icon}, recent[:min(len(recent), 4)]...)
	}
	return changed, nil
}

// write persists wf as is.
func (s *Store) write(wf *model.Workflow) error {
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, wf.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, workflowFile+".*.tmp")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, workflowFile))
}

// forget drops the cached List entry of workflow id.
func (s *Store) forget(id string) {
	s.summaryMu.Lock()
	delete(s.summaries, id)
	s.summaryMu.Unlock()
}

// Delete removes the workflow directory including its assets.
func (s *Store) Delete(id string) error {
	if !validID.MatchString(id) {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, id)
	if _, err := os.Stat(filepath.Join(dir, workflowFile)); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	s.forget(id)
	return nil
}

// AssetsDir is where non-Terraform files of a workflow (Lambda sources,
// templates, ...) live. It is not created by this call.
func (s *Store) AssetsDir(id string) string {
	return filepath.Join(s.dir, id, "assets")
}

var (
	slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)
	idEncoding  = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
)

// NewID returns "<slug>-<6 random base32 chars>", e.g. "orders-api-k3x9qa".
func NewID(name string) string {
	slug := strings.Trim(slugInvalid.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(slug) > 32 {
		slug = strings.TrimRight(slug[:32], "-")
	}
	if slug == "" {
		slug = "workflow"
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return slug + "-" + idEncoding.EncodeToString(b[:])[:6]
}
