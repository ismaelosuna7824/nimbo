package catalog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/go-version"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ProviderCompat is what Nimbo knows about the versions of one embedded
// provider (curated/provider_compat.json plus the embedded index).
type ProviderCompat struct {
	Name   string `json:"name"`   // "google"
	Source string `json:"source"` // "hashicorp/google" (from the index)
	// Catalog is the version the embedded schema was generated from (from
	// the index): what Nimbo validates attributes and blocks against.
	Catalog string `json:"catalog"`
	// LocalMinimum is the oldest release verified to work with Floci for
	// the services Nimbo emulates.
	LocalMinimum string `json:"localMinimum"`
	// Recommended is the constraint Nimbo writes: "~> <catalog
	// major>.<catalog minor>" (derived, see recommendedConstraint).
	Recommended string `json:"recommended"`
	// Issues are known-broken version ranges on the local emulators.
	Issues []CompatIssue `json:"issues,omitempty"`

	catalogVersion *version.Version
	minimum        *version.Version
}

// CompatIssue is a range of provider releases that is known to break some
// resources on the local emulators.
type CompatIssue struct {
	// Versions is the affected range as a Terraform constraint ("< 7.26.0").
	Versions string `json:"versions"`
	// Services are the resource types the range breaks (a trailing "*"
	// matches a prefix: "oci_functions_*"); empty = every resource of the
	// provider.
	Services []string `json:"services,omitempty"`
	// Message says what breaks locally; Fix what to change (the caller
	// adds the recommended constraint).
	Message string `json:"message"`
	Fix     string `json:"fix"`

	versions version.Constraints
}

// Affects reports whether the issue breaks one of types (the resource types
// a configuration declares).
func (i CompatIssue) Affects(types []string) bool {
	if len(i.Services) == 0 {
		return true
	}
	for _, s := range i.Services {
		prefix, glob := strings.CutSuffix(s, "*")
		for _, t := range types {
			if t == s || glob && strings.HasPrefix(t, prefix) {
				return true
			}
		}
	}
	return false
}

// Includes reports whether release v is inside the issue's range.
func (i CompatIssue) Includes(v *version.Version) bool { return i.versions.Check(v) }

// AffectsCatalog reports whether the catalog release itself is inside the
// issue's range: an emulator gap that no constraint Nimbo recommends avoids.
func (pc ProviderCompat) AffectsCatalog(is CompatIssue) bool { return is.Includes(pc.catalogVersion) }

// WithoutGaps is pc without the issues that contain the catalog release:
// what a version constraint can avoid by following the recommendation.
func (pc ProviderCompat) WithoutGaps() ProviderCompat {
	var issues []CompatIssue
	for _, is := range pc.Issues {
		if !pc.AffectsCatalog(is) {
			issues = append(issues, is)
		}
	}
	pc.Issues = issues
	return pc
}

// catalogGap returns the emulator gap (an issue containing the catalog
// release) that breaks typ locally.
func (c *Catalog) catalogGap(typ string) (CompatIssue, bool) {
	name, _, _ := strings.Cut(typ, "_")
	pc, ok := c.compat[name]
	if !ok {
		return CompatIssue{}, false
	}
	for _, is := range pc.Issues {
		if pc.AffectsCatalog(is) && is.Affects([]string{typ}) {
			return is, true
		}
	}
	return CompatIssue{}, false
}

// local reports whether typ runs in the local emulator with the catalog
// release: its service is supported and no emulator gap breaks it.
func (c *Catalog) local(cloud model.Cloud, typ string, si serviceInfo) bool {
	if !c.supported(cloud, si) {
		return false
	}
	_, gap := c.catalogGap(typ)
	return !gap
}

type compatFile struct {
	Providers []ProviderCompat `json:"providers"`
}

// loadCompat reads curated/provider_compat.json (see parseCompat).
func loadCompat(c *Catalog) (map[string]*ProviderCompat, error) {
	raw, err := curatedFS.ReadFile("curated/provider_compat.json")
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	return parseCompat(c, raw)
}

// parseCompat decodes the version table and completes it from the index;
// it must list exactly the embedded providers.
func parseCompat(c *Catalog, raw []byte) (map[string]*ProviderCompat, error) {
	var f compatFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("catalog: provider_compat.json: %w", err)
	}
	var err error
	out := make(map[string]*ProviderCompat, len(f.Providers))
	for i := range f.Providers {
		pc := &f.Providers[i]
		p, ok := c.byName[pc.Name]
		if !ok {
			return nil, fmt.Errorf("catalog: provider_compat.json: %s is not an embedded provider", pc.Name)
		}
		if _, dup := out[pc.Name]; dup {
			return nil, fmt.Errorf("catalog: provider_compat.json: %s listed twice", pc.Name)
		}
		pc.Source, pc.Catalog = p.info.Source, p.info.Version
		if pc.catalogVersion, err = version.NewVersion(pc.Catalog); err != nil {
			return nil, fmt.Errorf("catalog: %s catalog version: %w", pc.Name, err)
		}
		if pc.minimum, err = version.NewVersion(pc.LocalMinimum); err != nil {
			return nil, fmt.Errorf("catalog: provider_compat.json: %s localMinimum: %w", pc.Name, err)
		}
		pc.Recommended = recommendedConstraint(pc.catalogVersion)
		for j := range pc.Issues {
			is := &pc.Issues[j]
			if is.versions, err = version.NewConstraint(is.Versions); err != nil {
				return nil, fmt.Errorf("catalog: provider_compat.json: %s issue %q: %w", pc.Name, is.Versions, err)
			}
		}
		out[pc.Name] = pc
	}
	for _, p := range c.providers {
		if out[p.info.Name] == nil {
			return nil, fmt.Errorf("catalog: provider_compat.json lacks %s", p.info.Name)
		}
	}
	return out, nil
}

// recommendedConstraint is the constraint Nimbo writes for a provider whose
// schema it embeds at v: "~> <major>.<minor>". The lower bound is the schema
// release, so every attribute and block Nimbo validates and offers exists in
// whatever init installs; the upper bound stops before the next major,
// which renames and removes resources (and has no embedded schema).
func recommendedConstraint(v *version.Version) string {
	s := v.Segments()
	return fmt.Sprintf("~> %d.%d", s[0], s[1])
}

// ProviderCompat returns the version table entry of an embedded provider.
func (c *Catalog) ProviderCompat(name string) (ProviderCompat, bool) {
	pc, ok := c.compat[name]
	if !ok {
		return ProviderCompat{}, false
	}
	return pc.clone(), true
}

// ProviderCompats returns every embedded provider's entry, in catalog order.
func (c *Catalog) ProviderCompats() []ProviderCompat {
	out := make([]ProviderCompat, 0, len(c.providers))
	for _, p := range c.providers {
		out = append(out, c.compat[p.info.Name].clone())
	}
	return out
}

func (pc *ProviderCompat) clone() ProviderCompat {
	out := *pc
	out.Issues = slices.Clone(pc.Issues)
	return out
}

// Major is the catalog release's major version.
func (pc ProviderCompat) Major() int { return pc.catalogVersion.Segments()[0] }

// issuesFor returns the issues that break one of types.
func (pc ProviderCompat) issuesFor(types []string) []CompatIssue {
	var out []CompatIssue
	for _, is := range pc.Issues {
		if is.Affects(types) {
			out = append(out, is)
		}
	}
	return out
}

// VersionCheck is how one selected release (a lock file's) fares.
type VersionCheck struct {
	Invalid      bool
	BelowMinimum bool          // older than LocalMinimum
	OtherMajor   bool          // not the catalog's major: the schema differs
	Issues       []CompatIssue // known-broken ranges containing it that break one of the types
}

// CheckVersion evaluates the release v for a configuration declaring types.
func (pc ProviderCompat) CheckVersion(v string, types []string) VersionCheck {
	ver, err := version.NewVersion(v)
	if err != nil {
		return VersionCheck{Invalid: true}
	}
	out := VersionCheck{
		BelowMinimum: ver.LessThan(pc.minimum),
		OtherMajor:   ver.Segments()[0] != pc.Major(),
	}
	for _, is := range pc.issuesFor(types) {
		if is.Includes(ver) {
			out.Issues = append(out.Issues, is)
		}
	}
	return out
}

// ConstraintCheck is how a required_providers version constraint fares.
type ConstraintCheck struct {
	Invalid bool
	// OtherMajor: no release of the catalog's major satisfies it.
	OtherMajor bool
	// BelowMinimum: it allows releases older than LocalMinimum.
	BelowMinimum bool
	// Issues are the known-broken ranges (breaking one of the types) it
	// allows.
	Issues []CompatIssue
	// Broken: every release it allows is older than LocalMinimum or inside
	// one of Issues, whatever init selects.
	Broken bool
}

// OK reports a constraint without problems.
func (cc ConstraintCheck) OK() bool {
	return !cc.Invalid && !cc.OtherMajor && !cc.BelowMinimum && len(cc.Issues) == 0
}

// CheckConstraint evaluates a version constraint ("" = any release) for a
// configuration declaring types.
func (pc ProviderCompat) CheckConstraint(constraint string, types []string) ConstraintCheck {
	cs := version.Constraints{}
	if strings.TrimSpace(constraint) != "" {
		var err error
		if cs, err = version.NewConstraint(constraint); err != nil {
			return ConstraintCheck{Invalid: true}
		}
	}
	issues := pc.issuesFor(types)
	texts := []string{constraint, pc.LocalMinimum, pc.Catalog}
	for _, is := range issues {
		texts = append(texts, is.Versions)
	}
	probes := probeVersions(texts...)
	major := pc.Major()
	allows := func(pred func(v *version.Version) bool) bool {
		for _, v := range probes {
			if cs.Check(v) && pred(v) {
				return true
			}
		}
		return false
	}
	out := ConstraintCheck{
		OtherMajor:   !allows(func(v *version.Version) bool { return v.Segments()[0] == major }),
		BelowMinimum: allows(func(v *version.Version) bool { return v.LessThan(pc.minimum) }),
	}
	for _, is := range issues {
		if allows(is.Includes) {
			out.Issues = append(out.Issues, is)
		}
	}
	out.Broken = !allows(func(v *version.Version) bool {
		if v.LessThan(pc.minimum) {
			return false
		}
		for _, is := range issues {
			if is.Includes(v) {
				return false
			}
		}
		return true
	})
	return out
}

// Provider version status values (model.ProviderVersion.Status).
const (
	StatusOK            = "ok"
	StatusOutdated      = "outdated"
	StatusBrokenLocally = "broken-locally"
)

// Status summarizes a provider's constraint and selected release (locked,
// "" = not initialized yet): broken-locally when the release init selects
// (or every release the constraint allows) is in a known-broken range for
// these types; outdated when the constraint or the release misses the
// catalog's major or goes below LocalMinimum or into such a range; ok
// otherwise.
func (pc ProviderCompat) Status(constraint, locked string, types []string) string {
	cc := pc.CheckConstraint(constraint, types)
	if locked != "" {
		vc := pc.CheckVersion(locked, types)
		switch {
		case len(vc.Issues) > 0:
			return StatusBrokenLocally
		case vc.BelowMinimum || vc.OtherMajor || !cc.OK():
			return StatusOutdated
		}
		return StatusOK
	}
	switch {
	case cc.Broken && len(cc.Issues) > 0:
		return StatusBrokenLocally
	case !cc.OK():
		return StatusOutdated
	}
	return StatusOK
}

// versionLiteral matches the versions inside a constraint.
var versionLiteral = regexp.MustCompile(`\d+(?:\.\d+){0,2}`)

// probeHigh stands for "any later minor/patch".
const probeHigh = 1 << 20

// probeVersions returns versions that decide any conjunction of
// comparisons on the versions the texts mention: each mentioned version,
// the next and previous release, and the next minor and major (pessimistic
// bounds) with their neighbors. A constraint whose bounds are all among
// them allows some release inside a range bounded the same way iff it
// allows one of these.
func probeVersions(texts ...string) []*version.Version {
	seen := map[[3]int]bool{}
	var out []*version.Version
	add := func(s [3]int) {
		if s[0] < 0 || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, version.Must(version.NewVersion(fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]))))
	}
	around := func(s [3]int) {
		add(s)
		add([3]int{s[0], s[1], s[2] + 1})
		switch {
		case s[2] > 0:
			add([3]int{s[0], s[1], s[2] - 1})
		case s[1] > 0:
			add([3]int{s[0], s[1] - 1, probeHigh})
		case s[0] > 0:
			add([3]int{s[0] - 1, probeHigh, probeHigh})
		}
	}
	around([3]int{0, 0, 0})
	around([3]int{probeHigh, 0, 0})
	for _, t := range texts {
		for _, lit := range versionLiteral.FindAllString(t, -1) {
			var s [3]int
			for i, part := range strings.SplitN(lit, ".", 3) {
				s[i], _ = strconv.Atoi(part)
			}
			around(s)
			around([3]int{s[0], s[1] + 1, 0})
			around([3]int{s[0] + 1, 0, 0})
		}
	}
	return out
}

// RequiredProvider is one terraform.required_providers entry of a
// workflow's root module.
type RequiredProvider struct {
	Local      string // local name: "google"
	Source     string // "hashicorp/google" ("" when not set)
	Type       string // provider type: the source's last part, else Local
	Constraint string // version constraint ("" = none)
}

// RequiredProviders lists wf's required_providers entries in order. The
// legacy form (aws = "~> 5.0") is a constraint.
func RequiredProviders(wf *model.Workflow) []RequiredProvider {
	if wf.Terraform == nil {
		return nil
	}
	var out []RequiredProvider
	for _, blk := range wf.Terraform.Blocks {
		if blk.Type != "required_providers" {
			continue
		}
		for _, a := range blk.Body.Attrs {
			rp := RequiredProvider{Local: a.Name, Type: a.Name}
			raw := a.Value.Literal
			if len(raw) == 0 && a.Value.Expr != "" {
				raw, _ = hclcodec.LiteralFromExpr(a.Value.Expr)
			}
			var v any
			_ = json.Unmarshal(raw, &v)
			switch v := v.(type) {
			case string:
				rp.Constraint = v
			case map[string]any:
				rp.Source, _ = v["source"].(string)
				rp.Constraint, _ = v["version"].(string)
			}
			if rp.Source != "" {
				rp.Type = rp.Source[strings.LastIndex(rp.Source, "/")+1:]
			}
			out = append(out, rp)
		}
	}
	return out
}

// CompatFor returns the table entry for a required provider: its type
// names an embedded provider and its source, when set, is that provider's.
func (c *Catalog) CompatFor(rp RequiredProvider) (ProviderCompat, bool) {
	pc, ok := c.ProviderCompat(rp.Type)
	if !ok {
		return pc, false
	}
	if rp.Source != "" && !strings.EqualFold(strings.TrimPrefix(rp.Source, "registry.terraform.io/"), pc.Source) {
		return ProviderCompat{}, false
	}
	return pc, true
}

// ResourceTypes lists the distinct resource types wf declares.
func ResourceTypes(wf *model.Workflow) []string {
	var out []string
	for i := range wf.Nodes {
		if n := &wf.Nodes[i]; n.Kind == model.KindResource && !slices.Contains(out, n.Type) {
			out = append(out, n.Type)
		}
	}
	return out
}

// ProviderVersions reports each required provider of wf that the catalog
// embeds: its constraint, the release locked (lock maps provider type to
// the version .terraform.lock.hcl selected), the recommended constraint
// and a status.
func (c *Catalog) ProviderVersions(wf *model.Workflow, lock map[string]string) []model.ProviderVersion {
	types := ResourceTypes(wf)
	var out []model.ProviderVersion
	for _, rp := range RequiredProviders(wf) {
		pc, ok := c.CompatFor(rp)
		if !ok {
			continue
		}
		out = append(out, model.ProviderVersion{
			Name: rp.Local, Constraint: rp.Constraint, Locked: lock[rp.Type],
			Recommended: pc.Recommended, Status: pc.Status(rp.Constraint, lock[rp.Type], types),
		})
	}
	return out
}
