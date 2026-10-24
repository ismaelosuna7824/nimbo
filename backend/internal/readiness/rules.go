package readiness

import (
	"fmt"
	"slices"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Check is a checklist item made of best-practice rules.
type Check struct {
	ID, Title string
	Codes     []string
	// ProdOnly: the rules only run in production-like environments.
	ProdOnly bool
	// Pass is the message when nothing is found.
	Pass string
}

// Findings are an environment's diagnostics by rule code, and which codes
// an item claimed.
type Findings struct {
	All      []model.Diagnostic
	byCode   map[string][]model.Diagnostic
	used     map[string]bool
	disabled []string
	prodLike bool
}

// NewFindings indexes ds; disabled are the rules turned off in Settings.
func NewFindings(ds []model.Diagnostic, disabled []string, prodLike bool) *Findings {
	f := &Findings{All: ds, byCode: map[string][]model.Diagnostic{}, used: map[string]bool{}, disabled: disabled, prodLike: prodLike}
	for _, d := range ds {
		f.byCode[d.Code] = append(f.byCode[d.Code], d)
	}
	return f
}

// Items builds one item per check.
func (f *Findings) Items(checks []Check) []model.ReadinessItem {
	out := make([]model.ReadinessItem, 0, len(checks))
	for _, c := range checks {
		out = append(out, f.Item(c))
	}
	return out
}

// Item builds an item from the findings of its rules.
func (f *Findings) Item(c Check) model.ReadinessItem {
	item := model.ReadinessItem{ID: c.ID, Title: c.Title, Codes: c.Codes}
	for _, code := range c.Codes {
		f.used[code] = true
		item.Diagnostics = append(item.Diagnostics, f.byCode[code]...)
	}
	SortDiagnostics(item.Diagnostics)
	return f.Finish(item, c)
}

// Claim folds the findings no item claimed (codes of other tools) into
// item, so nothing is silently dropped.
func (f *Findings) Claim(item *model.ReadinessItem, c Check) {
	var rest []model.Diagnostic
	for _, d := range f.All {
		if !f.used[d.Code] {
			rest = append(rest, d)
		}
	}
	if len(rest) == 0 {
		return
	}
	item.Diagnostics = append(item.Diagnostics, rest...)
	SortDiagnostics(item.Diagnostics)
	*item = f.Finish(*item, c)
}

// Node returns the findings of graph node id.
func (f *Findings) Node(id string) []model.Diagnostic {
	var out []model.Diagnostic
	for _, d := range f.All {
		if d.NodeID == id {
			out = append(out, d)
		}
	}
	return out
}

// Finish sets an item's status, message and action from its diagnostics.
func (f *Findings) Finish(item model.ReadinessItem, c Check) model.ReadinessItem {
	if len(item.Diagnostics) == 0 {
		item.Action = nil
		switch {
		case len(c.Codes) > 0 && f.AllDisabled(c.Codes):
			item.Status, item.Message = model.ItemSkip, "Turned off in Settings."
		case c.ProdOnly && !f.prodLike:
			item.Status, item.Message = model.ItemSkip, "Checked in production-like environments only."
		default:
			item.Status, item.Message = model.ItemPass, c.Pass
		}
		return item
	}
	item.Status = severityStatus[item.Diagnostics[0].Severity] // sorted worst first
	item.Message = item.Diagnostics[0].Message
	if n := len(item.Diagnostics) - 1; n > 0 {
		item.Message += fmt.Sprintf(" (+%d more)", n)
	}
	item.Action = &model.ReadinessAction{Kind: model.ActionOpenProblem, Label: "Open problem"}
	return item
}

// AllDisabled reports whether every code is turned off in Settings.
func (f *Findings) AllDisabled(codes []string) bool {
	for _, c := range codes {
		if !slices.Contains(f.disabled, c) {
			return false
		}
	}
	return true
}

// Settings are the rule settings of a report: every opt-in rule of rules
// is on (they only add info items), rules turned off in Settings stay off.
func Settings(cfg model.BestPracticeSettings, rules []model.BestPracticeRule) model.BestPracticeSettings {
	out := model.BestPracticeSettings{DisabledRules: slices.Clone(cfg.DisabledRules)}
	for _, r := range rules {
		if r.DefaultOff {
			out.EnabledRules = append(out.EnabledRules, r.Code)
		}
	}
	return out
}
