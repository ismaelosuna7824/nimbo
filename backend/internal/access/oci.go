package access

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// OCI policies: the `statements` of an oci_identity_policy, one sentence
// per list element, in Oracle's policy syntax
// (https://docs.oracle.com/en-us/iaas/Content/Identity/Concepts/policysyntax.htm):
//
//	Allow <group|dynamic-group|any-user|any-group|service> <subjects> to <verb> <resource-type>
//	  in <tenancy | compartment <name|a:b path> | compartment id <ocid>> [where <conditions>]
//
// Elements are HCL template strings: ${...} interpolations stay as written
// (subjects and compartments are often "${oci_identity_group.g.name}").
// Endorse/Admit/Define statements, permission lists ({PERM, ...}), other
// expressions and anything else the grammar doesn't cover are read-only and
// kept byte for byte.

// OCIPolicyType is the Terraform type of an OCI policy.
const OCIPolicyType = "oci_identity_policy"

type ociProvider struct{}

func init() { Register(ociProvider{}) }

func (ociProvider) Name() string { return "oci" }

func (ociProvider) Supports(n *model.Node) bool {
	return n.Kind == model.KindResource && n.Type == OCIPolicyType
}

func (ociProvider) View(_ *model.Workflow, n *model.Node) (*model.AccessView, error) {
	pol := ReadOCIPolicy(&n.Body)
	return &model.AccessView{OCI: pol, Warnings: OCIWarnings(pol)}, nil
}

func (ociProvider) Apply(_ *model.Workflow, n *model.Node, edit model.AccessEdit) error {
	if edit.OCI == nil {
		return Editf("access: %s is an OCI policy: send its statements in \"oci\"", Address(n))
	}
	return WriteOCIPolicy(&n.Body, edit.OCI.Statements)
}

// OCI verbs, from least to most access.
var ociVerbs = []string{"inspect", "read", "use", "manage"}

var ociSubjectTypes = []string{"group", "dynamic-group", "any-user", "any-group", "service"}

// ReadOCIPolicy parses the statements attribute of an oci_identity_policy
// body. A missing attribute is an editable empty policy; a value that is
// not a list written in place (a variable, local, function call) is
// read-only.
func ReadOCIPolicy(b *model.Body) *model.OCIPolicy {
	pol := &model.OCIPolicy{Statements: []model.OCIStatement{}, Editable: true}
	a := GetAttr(b, "statements")
	if a == nil {
		return pol
	}
	src := strings.TrimSpace(Source(a.Value))
	elems, ok := ociElements(src)
	if !ok {
		reason := "statements is not a list written here (it comes from " + src + "): edit it in code."
		pol.Editable, pol.Reason = false, reason
		pol.Statements = append(pol.Statements, model.OCIStatement{Source: src, Reason: reason})
		return pol
	}
	for _, r := range elems {
		pol.Statements = append(pol.Statements, ParseOCIElement(src[r[0]:r[1]]))
	}
	return pol
}

// ociElements returns the byte range of each element of a list
// constructor in src.
func ociElements(src string) ([][2]int, bool) {
	if src == "" {
		return nil, true
	}
	e, diags := hclsyntax.ParseExpression([]byte(src), "statements.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, false
	}
	tup, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, false
	}
	out := make([][2]int, len(tup.Exprs))
	for i, el := range tup.Exprs {
		out[i] = [2]int{el.Range().Start.Byte, el.Range().End.Byte}
	}
	return out, true
}

// ParseOCIElement parses one list element (HCL source). Anything it can't
// represent is returned read-only with Source set.
func ParseOCIElement(src string) model.OCIStatement {
	ro := func(reason string) model.OCIStatement {
		return model.OCIStatement{Source: src, Reason: reason}
	}
	text, ok := ociTemplateText(src)
	if !ok {
		return ro("This statement is built by an expression, not written as a sentence.")
	}
	st, reason := ParseOCIStatement(text)
	if reason != "" {
		return ro(reason)
	}
	st.Source = src
	return st
}

// ociTemplateText is the text of a quoted HCL string element with its
// ${...} interpolations kept as written; false for other expressions,
// template directives (%{...}), escaped interpolations ($${) and escape
// sequences other than \" and \\.
func ociTemplateText(src string) (string, bool) {
	if len(src) < 2 || src[0] != '"' || src[len(src)-1] != '"' {
		return "", false
	}
	e, diags := hclsyntax.ParseExpression([]byte(src), "statement.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		return "", false
	}
	switch e.(type) {
	case *hclsyntax.TemplateExpr, *hclsyntax.TemplateWrapExpr, *hclsyntax.LiteralValueExpr:
	default:
		return "", false
	}
	inner := src[1 : len(src)-1]
	var sb strings.Builder
	for i := 0; i < len(inner); {
		switch {
		case strings.HasPrefix(inner[i:], "${"):
			end := interpEnd(inner, i)
			if end < 0 {
				return "", false
			}
			sb.WriteString(inner[i:end])
			i = end
		case strings.HasPrefix(inner[i:], "$${"), strings.HasPrefix(inner[i:], "%{"), strings.HasPrefix(inner[i:], "%%{"):
			return "", false
		case inner[i] == '\\':
			if i+1 >= len(inner) || inner[i+1] != '"' && inner[i+1] != '\\' {
				return "", false
			}
			sb.WriteByte(inner[i+1])
			i += 2
		case inner[i] == '\n':
			return "", false
		default:
			sb.WriteByte(inner[i])
			i++
		}
	}
	return sb.String(), true
}

// interpEnd returns the index just past the "}" closing the interpolation
// starting at s[i] ("${"), or -1. Strings inside it are skipped.
func interpEnd(s string, i int) int {
	depth := 0
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j + 1
			}
		case '"':
			for j++; j < len(s) && s[j] != '"'; j++ {
				if s[j] == '\\' {
					j++
				}
			}
			if j >= len(s) {
				return -1
			}
		}
	}
	return -1
}

// QuoteOCITemplate renders statement text as an HCL template string:
// interpolations stay as they are, quotes and backslashes elsewhere are
// escaped.
func QuoteOCITemplate(text string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "${") {
			if end := interpEnd(text, i); end > 0 {
				sb.WriteString(text[i:end])
				i = end
				continue
			}
		}
		switch {
		case text[i] == '"' || text[i] == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(text[i])
		case strings.HasPrefix(text[i:], "%{"):
			sb.WriteString("%%")
			i++
			continue
		case strings.HasPrefix(text[i:], "${"): // unterminated: literal
			sb.WriteString("$$")
			i++
			continue
		default:
			sb.WriteByte(text[i])
		}
		i++
	}
	sb.WriteByte('"')
	return sb.String()
}

// ociToken is one word of a statement; Start/End index the text.
type ociToken struct {
	s          string
	start, end int
}

func (t ociToken) is(word string) bool { return strings.EqualFold(t.s, word) }

// ociTokens splits a statement into words: whitespace separates them, ","
// is a token of its own, and ${...}, '...', "..." and {...} never split.
func ociTokens(text string) ([]ociToken, bool) {
	var out []ociToken
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out = append(out, ociToken{s: text[start:end], start: start, end: end})
			start = -1
		}
	}
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			flush(i)
			i++
			continue
		case c == ',':
			flush(i)
			out = append(out, ociToken{s: ",", start: i, end: i + 1})
			i++
			continue
		}
		if start < 0 {
			start = i
		}
		switch {
		case strings.HasPrefix(text[i:], "${"):
			end := interpEnd(text, i)
			if end < 0 {
				return nil, false
			}
			i = end
		case c == '\'' || c == '"':
			j := strings.IndexByte(text[i+1:], c)
			if j < 0 {
				return nil, false
			}
			i += j + 2
		case c == '{':
			depth := 0
			j := i
			for ; j < len(text); j++ {
				if text[j] == '{' {
					depth++
				} else if text[j] == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if j >= len(text) {
				return nil, false
			}
			i = j + 1
		default:
			i++
		}
	}
	flush(len(text))
	return out, true
}

// ParseOCIStatement parses statement text; reason is set (and the
// statement empty) when the grammar doesn't cover it.
func ParseOCIStatement(text string) (st model.OCIStatement, reason string) {
	toks, ok := ociTokens(text)
	if !ok {
		return st, "Unbalanced quotes, braces or ${...} in this statement."
	}
	if len(toks) == 0 {
		return st, "Empty statement."
	}
	switch strings.ToLower(toks[0].s) {
	case "allow":
	case "endorse", "admit", "define":
		return st, "Cross-tenancy statements (" + toks[0].s + ") are kept as written."
	default:
		return st, "Not an Allow statement."
	}
	i := 1
	next := func() (ociToken, bool) {
		if i >= len(toks) {
			return ociToken{}, false
		}
		i++
		return toks[i-1], true
	}
	t, ok := next()
	if !ok || !slices.Contains(ociSubjectTypes, strings.ToLower(t.s)) {
		return st, "Unknown subject: statements start with Allow group, dynamic-group, any-user, any-group or service."
	}
	st.SubjectType = strings.ToLower(t.s)
	// Subjects up to "to", separated by commas ("id <ocid>" is one subject).
	var cur []string
	for {
		t, ok = next()
		if !ok {
			return model.OCIStatement{}, "Missing \"to <verb>\"."
		}
		if t.is("to") {
			break
		}
		if t.s == "," {
			if len(cur) == 0 {
				return model.OCIStatement{}, "Empty subject in the list."
			}
			st.Subjects = append(st.Subjects, strings.Join(cur, " "))
			cur = nil
			continue
		}
		cur = append(cur, t.s)
	}
	if len(cur) > 0 {
		st.Subjects = append(st.Subjects, strings.Join(cur, " "))
	} else if len(st.Subjects) > 0 {
		return model.OCIStatement{}, "Empty subject in the list."
	}
	anyone := st.SubjectType == "any-user" || st.SubjectType == "any-group"
	if anyone != (len(st.Subjects) == 0) {
		if anyone {
			return model.OCIStatement{}, st.SubjectType + " takes no names."
		}
		return model.OCIStatement{}, "Missing the " + st.SubjectType + " name."
	}
	t, ok = next()
	if ok && strings.HasPrefix(t.s, "{") {
		return model.OCIStatement{}, "Permission lists ({PERMISSION, ...}) are kept as written."
	}
	if !ok || !slices.Contains(ociVerbs, strings.ToLower(t.s)) {
		return model.OCIStatement{}, "Unknown verb: use inspect, read, use or manage."
	}
	st.Verb = strings.ToLower(t.s)
	t, ok = next()
	if !ok || t.is("in") {
		return model.OCIStatement{}, "Missing the resource-type."
	}
	if strings.HasPrefix(t.s, "{") {
		return model.OCIStatement{}, "Permission lists ({PERMISSION, ...}) are kept as written."
	}
	st.ResourceType = t.s
	if t, ok = next(); !ok || !t.is("in") {
		return model.OCIStatement{}, "Missing \"in <location>\" after the resource-type."
	}
	t, ok = next()
	switch {
	case ok && t.is("tenancy"):
		st.Location = "tenancy"
	case ok && t.is("compartment"):
		st.Location = "compartment"
		c, ok := next()
		if !ok || c.is("where") {
			return model.OCIStatement{}, "Missing the compartment name."
		}
		st.Compartment = c.s
		if c.is("id") {
			id, ok := next()
			if !ok {
				return model.OCIStatement{}, "Missing the compartment OCID."
			}
			st.Compartment = "id " + id.s
		}
	default:
		return model.OCIStatement{}, "The location must be tenancy or compartment <name>."
	}
	if t, ok = next(); ok {
		if !t.is("where") || i >= len(toks) {
			return model.OCIStatement{}, "Unexpected words after the location."
		}
		st.Condition = strings.TrimSpace(text[toks[i].start:])
	}
	st.Editable = true
	return st, ""
}

// OCIStatementText renders a statement as policy text (interpolations
// kept).
func OCIStatementText(st model.OCIStatement) string {
	var sb strings.Builder
	sb.WriteString("Allow ")
	sb.WriteString(st.SubjectType)
	if len(st.Subjects) > 0 {
		sb.WriteString(" ")
		sb.WriteString(strings.Join(st.Subjects, ", "))
	}
	sb.WriteString(" to ")
	sb.WriteString(st.Verb)
	sb.WriteString(" ")
	sb.WriteString(st.ResourceType)
	sb.WriteString(" in ")
	if st.Location == "tenancy" {
		sb.WriteString("tenancy")
	} else {
		sb.WriteString("compartment ")
		sb.WriteString(st.Compartment)
	}
	if c := strings.TrimSpace(st.Condition); c != "" {
		sb.WriteString(" where ")
		sb.WriteString(c)
	}
	return sb.String()
}

// normalizeOCI trims the parts of an edited statement and lowercases its
// keywords.
func normalizeOCI(st model.OCIStatement) model.OCIStatement {
	st.SubjectType = strings.ToLower(strings.TrimSpace(st.SubjectType))
	st.Verb = strings.ToLower(strings.TrimSpace(st.Verb))
	st.Location = strings.ToLower(strings.TrimSpace(st.Location))
	st.ResourceType = strings.TrimSpace(st.ResourceType)
	st.Compartment = strings.TrimSpace(st.Compartment)
	st.Condition = strings.TrimSpace(st.Condition)
	subjects := make([]string, 0, len(st.Subjects))
	for _, s := range st.Subjects {
		if s = strings.TrimSpace(s); s != "" {
			subjects = append(subjects, s)
		}
	}
	st.Subjects = subjects
	return st
}

// RenderOCIStatement validates st and returns its list element source.
func RenderOCIStatement(st model.OCIStatement) (string, error) {
	st = normalizeOCI(st)
	if !slices.Contains(ociSubjectTypes, st.SubjectType) {
		return "", Editf("unknown subject type %q: use group, dynamic-group, any-user, any-group or service", st.SubjectType)
	}
	anyone := st.SubjectType == "any-user" || st.SubjectType == "any-group"
	if anyone && len(st.Subjects) > 0 {
		return "", Editf("%s takes no names", st.SubjectType)
	}
	if !anyone && len(st.Subjects) == 0 {
		return "", Editf("choose the %s the statement allows", st.SubjectType)
	}
	if !slices.Contains(ociVerbs, st.Verb) {
		return "", Editf("unknown verb %q: use inspect, read, use or manage", st.Verb)
	}
	if st.ResourceType == "" || strings.ContainsAny(st.ResourceType, " \t{},") {
		return "", Editf("invalid resource-type %q", st.ResourceType)
	}
	switch st.Location {
	case "tenancy":
		st.Compartment = ""
	case "compartment":
		if st.Compartment == "" {
			return "", Editf("choose the compartment the statement applies in")
		}
	default:
		return "", Editf("location must be tenancy or compartment, not %q", st.Location)
	}
	text := OCIStatementText(st)
	if strings.ContainsAny(text, "\r\n") {
		return "", Editf("a statement is one line")
	}
	// The sentence must read back the same (no stray "to"/"in"/commas).
	back, reason := ParseOCIStatement(text)
	if reason != "" {
		return "", Editf("statement %q: %s", text, reason)
	}
	if !sameOCI(back, st) {
		return "", Editf("statement %q does not read back as written: check the names for spaces, commas or keywords (to, in, where)", text)
	}
	src := QuoteOCITemplate(text)
	if _, diags := hclsyntax.ParseExpression([]byte(src), "statement.hcl", hcl.InitialPos); diags.HasErrors() {
		return "", Editf("statement %q is not a valid HCL string: %s", text, diags.Error())
	}
	return src, nil
}

// sameOCI reports whether two parsed statements say the same.
func sameOCI(a, b model.OCIStatement) bool {
	a, b = normalizeOCI(a), normalizeOCI(b)
	return a.SubjectType == b.SubjectType && slices.Equal(a.Subjects, b.Subjects) && a.Verb == b.Verb &&
		a.ResourceType == b.ResourceType && a.Location == b.Location && a.Compartment == b.Compartment && a.Condition == b.Condition
}

// WriteOCIPolicy makes the statements attribute of b hold stmts. A
// statement whose Source is an element of the current list and whose
// fields are unchanged is written back byte for byte; edited and new
// statements are rendered as template strings. Read-only statements can be
// moved but neither changed nor removed (they are deleted in code). When
// the existing statements keep their order (edits in place, new ones at
// the end) the list is patched in place, keeping its comments and layout.
func WriteOCIPolicy(b *model.Body, stmts []model.OCIStatement) error {
	cur := ReadOCIPolicy(b)
	if !cur.Editable {
		return Editf("access: these statements can't be edited visually: %s", cur.Reason)
	}
	used := make([]bool, len(cur.Statements))
	// take returns the first unused original element with source src.
	take := func(src string) int {
		for j, o := range cur.Statements {
			if !used[j] && src != "" && o.Source == src {
				used[j] = true
				return j
			}
		}
		return -1
	}
	items := make([]string, len(stmts))
	from := make([]int, len(stmts))
	for i, st := range stmts {
		j := take(st.Source)
		from[i] = j
		switch {
		case j >= 0 && !cur.Statements[j].Editable:
			if st.Editable {
				return Editf("statement %d is read-only (%s): edit it in code", i+1, cur.Statements[j].Reason)
			}
			items[i] = st.Source
		case !st.Editable:
			return Editf("statement %d is read-only and not part of this policy: add it in code", i+1)
		case j >= 0 && sameOCI(cur.Statements[j], st):
			items[i] = st.Source
		default:
			src, err := RenderOCIStatement(st)
			if err != nil {
				return Editf("statement %d: %s", i+1, err.Error())
			}
			items[i] = src
		}
	}
	for j, o := range cur.Statements {
		if !used[j] && !o.Editable {
			return Editf("statement %s is shown read-only and can't be removed here: delete it in code", o.Source)
		}
	}
	a := GetAttr(b, "statements")
	if a == nil {
		SetExpr(b, "statements", ociList(items))
		return nil
	}
	src := strings.TrimSpace(Source(a.Value))
	ranges, _ := ociElements(src)
	inPlace := len(ranges) > 0 && len(items) >= len(ranges)
	for i := range from {
		if i < len(ranges) && from[i] != i || i >= len(ranges) && from[i] >= 0 {
			inPlace = false
		}
	}
	if !inPlace {
		if len(ranges) == 0 && len(items) == 0 {
			return nil
		}
		SetExpr(b, "statements", ociList(items))
		return nil
	}
	out := src
	if extra := items[len(ranges):]; len(extra) > 0 {
		end := ranges[len(ranges)-1][1]
		indent := "  "
		if nl := strings.LastIndexByte(src[:ranges[len(ranges)-1][0]], '\n'); nl >= 0 {
			if lead := src[nl+1 : ranges[len(ranges)-1][0]]; strings.TrimSpace(lead) == "" {
				indent = lead
			}
		}
		out = out[:end] + ",\n" + indent + strings.Join(extra, ",\n"+indent) + out[end:]
	}
	for k := len(ranges) - 1; k >= 0; k-- {
		if r := ranges[k]; out[r[0]:r[1]] != items[k] {
			out = out[:r[0]] + items[k] + out[r[1]:]
		}
	}
	if out != src {
		SetExpr(b, "statements", out)
	}
	return nil
}

func ociList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	return "[\n  " + strings.Join(items, ",\n  ") + ",\n]"
}

// AppendOCIStatements adds stmts to the policy in b, keeping every
// existing statement byte for byte; statements it already has (same
// sentence) are not added twice. It reports whether anything was added.
func AppendOCIStatements(b *model.Body, stmts []model.OCIStatement) (bool, error) {
	cur := ReadOCIPolicy(b)
	if !cur.Editable {
		return false, Editf("access: the statements of this policy can't be edited visually: %s", cur.Reason)
	}
	all := slices.Clone(cur.Statements)
	added := false
	for _, st := range stmts {
		dup := slices.ContainsFunc(all, func(o model.OCIStatement) bool { return o.Editable && sameOCI(o, st) })
		if !dup {
			st.Source, st.Editable = "", true
			all = append(all, st)
			added = true
		}
	}
	if !added {
		return false, nil
	}
	return true, WriteOCIPolicy(b, all)
}

// ---------------------------------------------------------------------------
// Warnings

// ociUseSuffices are resource-types whose day-to-day work (attaching
// instances to subnets, encrypting with a key, reading a vault) needs only
// the use verb; manage adds creating, deleting and reconfiguring them
// (https://docs.oracle.com/en-us/iaas/Content/Identity/policieshow/Verbs.htm,
// Common Policies "Let users launch compute instances").
var ociUseSuffices = map[string]string{
	"virtual-network-family": "use lets the group attach instances to existing VCNs and subnets; manage also creates, deletes and reconfigures the network.",
	"vaults":                 "use lets the group work with the vault's keys and secrets; manage also creates and deletes vaults.",
	"keys":                   "use lets the group encrypt and decrypt with keys; manage also creates, rotates and deletes them.",
}

// OCIWarnings lists the least-privilege warnings of a policy.
func OCIWarnings(pol *model.OCIPolicy) []model.AccessWarning {
	out := []model.AccessWarning{}
	for i, st := range pol.Statements {
		if !st.Editable {
			continue
		}
		path := fmt.Sprintf("statements/%d", i)
		where := "the whole tenancy"
		if st.Location == "compartment" {
			where = "compartment " + st.Compartment
		}
		if st.Verb == "manage" && strings.EqualFold(st.ResourceType, "all-resources") {
			out = append(out, model.AccessWarning{
				Code: "oci_manage_all", Severity: "warning", Path: path,
				Message: fmt.Sprintf("Lets %s do anything with every resource in %s: administrator access.", ociWho(st), where),
				Fix:     "Grant only the families the team works with (for example manage instance-family, use virtual-network-family).",
			})
		}
		if st.Location == "tenancy" && !ociAdmins(st) {
			out = append(out, model.AccessWarning{
				Code: "oci_tenancy_scope", Severity: "warning", Path: path,
				Message: fmt.Sprintf("Applies in the whole tenancy: %s gets it in every compartment.", ociWho(st)),
				Fix:     "Scope it to the compartment the resources live in: in compartment <name>.",
			})
		}
		if st.SubjectType == "any-user" && st.Condition == "" {
			out = append(out, model.AccessWarning{
				Code: "oci_any_user", Severity: "warning", Path: path,
				Message: "any-user without a where condition allows every user and resource principal of the tenancy.",
				Fix:     "Use a group, or add a condition (where request.principal.type = '...', where request.user.id = '...').",
			})
		}
		if why, ok := ociUseSuffices[st.ResourceType]; ok && st.Verb == "manage" {
			out = append(out, model.AccessWarning{
				Code: "oci_manage_verb", Severity: "info", Path: path,
				Message: fmt.Sprintf("manage %s may be more than needed: %s", st.ResourceType, why),
				Fix:     fmt.Sprintf("Use use %s unless the group administers it.", st.ResourceType),
			})
		}
	}
	return out
}

func ociWho(st model.OCIStatement) string {
	if len(st.Subjects) == 0 {
		return st.SubjectType
	}
	return st.SubjectType + " " + strings.Join(st.Subjects, ", ")
}

// ociAdmins: the tenancy's Administrators group, which is meant to have
// tenancy-wide access.
func ociAdmins(st model.OCIStatement) bool {
	if st.SubjectType != "group" {
		return false
	}
	for _, s := range st.Subjects {
		s = strings.Trim(s, "'")
		if _, name, ok := strings.Cut(s, "/"); ok {
			s = strings.Trim(name, "'")
		}
		if strings.EqualFold(s, "Administrators") {
			return true
		}
	}
	return false
}
