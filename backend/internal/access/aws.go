package access

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// AWS access editors: the IAM policy documents of identity policies
// (aws_iam_role_policy, aws_iam_policy, aws_iam_user_policy,
// aws_iam_group_policy), roles (trust policy, inline_policy blocks), data
// "aws_iam_policy_document" statement blocks and resource policies
// (aws_s3_bucket_policy, aws_sqs_queue_policy, aws_sns_topic_policy).
//
// Encodings (model.AWSPolicyDocument.Encoding):
//   - "jsonencode": jsonencode({...}) walked as HCL syntax; statements
//     that are not object literals or use for/conditionals are read-only.
//   - "json": a JSON string or heredoc; ${...} interpolations survive as
//     HCL template expressions. Written back as the same kind of string
//     (same heredoc delimiter).
//   - "document": the statement blocks of a data "aws_iam_policy_document".
//   - "reference": policy = data.aws_iam_policy_document.x.json (Linked;
//     edit the document there).
//   - "unsupported": anything else, read-only.
//
// Writes keep untouched statements byte for byte (matched by Source and
// fields), the statement order of the edit and every other key verbatim.

type awsProvider struct{}

func init() { Register(awsProvider{}) }

func (awsProvider) Name() string { return "aws" }

// awsPolicyLabels are the resource types whose "policy" attribute is one
// document, with its label.
var awsPolicyLabels = map[string]string{
	"aws_iam_policy":       "Permissions",
	"aws_iam_role_policy":  "Permissions",
	"aws_iam_user_policy":  "Permissions",
	"aws_iam_group_policy": "Permissions",
	"aws_s3_bucket_policy": "Bucket policy",
	"aws_sqs_queue_policy": "Queue policy",
	"aws_sns_topic_policy": "Topic policy",
}

func (awsProvider) Supports(n *model.Node) bool {
	switch n.Kind {
	case model.KindData:
		return n.Type == "aws_iam_policy_document"
	case model.KindResource:
		_, ok := awsPolicyLabels[n.Type]
		return ok || n.Type == "aws_iam_role"
	}
	return false
}

func awsNodes(wf *model.Workflow) []*model.Node {
	out := make([]*model.Node, len(wf.Nodes))
	for i := range wf.Nodes {
		out[i] = &wf.Nodes[i]
	}
	return out
}

func (awsProvider) View(wf *model.Workflow, n *model.Node) (*model.AccessView, error) {
	nodes := awsNodes(wf)
	docs := AWSDocuments(nodes, n)
	v := &model.AccessView{NodeID: n.ID, Provider: "aws", AWS: docs, Related: awsRelated(nodes, n), Warnings: []model.AccessWarning{}}
	for _, d := range docs {
		v.Warnings = append(v.Warnings, AWSWarnings(d)...)
	}
	return v, nil
}

func (awsProvider) Apply(wf *model.Workflow, n *model.Node, edit model.AccessEdit) error {
	if edit.AWS == nil {
		return Editf("access: the edit of %s has no aws document", Address(n))
	}
	return AWSWrite(awsNodes(wf), n, *edit.AWS)
}

// ---------------------------------------------------------------------------
// Documents of a node

// awsDocSpec is one document slot of a node.
type awsDocSpec struct {
	path, label string
	body        *model.Body // holder of the attribute (nil for "document")
	attr        string
}

var awsInlinePathRe = regexp.MustCompile(`^inline_policy\[(\d+)\]\.policy$`)

func awsSpecs(n *model.Node) []awsDocSpec {
	if n.Kind == model.KindData {
		if n.Type == "aws_iam_policy_document" {
			return []awsDocSpec{{path: "", label: "Policy document"}}
		}
		return nil
	}
	if l, ok := awsPolicyLabels[n.Type]; ok {
		return []awsDocSpec{{path: "policy", label: l, body: &n.Body, attr: "policy"}}
	}
	if n.Type != "aws_iam_role" {
		return nil
	}
	out := []awsDocSpec{{path: "assume_role_policy", label: "Trust policy", body: &n.Body, attr: "assume_role_policy"}}
	for i, b := range Blocks(&n.Body, "inline_policy") {
		label := "Inline policy"
		if a := GetAttr(&b.Body, "name"); a != nil {
			if s, ok := awsConstStringSrc(Source(a.Value)); ok && s != "" {
				label += " " + s
			}
		}
		out = append(out, awsDocSpec{path: fmt.Sprintf("inline_policy[%d].policy", i), label: label, body: &b.Body, attr: "policy"})
	}
	return out
}

// AWSDocuments reads the policy documents of n; nodes are the workflow's
// nodes (references to policy documents resolve among them).
func AWSDocuments(nodes []*model.Node, n *model.Node) []model.AWSPolicyDocument {
	var out []model.AWSPolicyDocument
	for _, s := range awsSpecs(n) {
		out = append(out, awsParseSpec(nodes, n, s).doc)
	}
	return out
}

// AWSDocument reads the document at path of n.
func AWSDocument(nodes []*model.Node, n *model.Node, path string) (model.AWSPolicyDocument, bool) {
	for _, s := range awsSpecs(n) {
		if s.path == path {
			return awsParseSpec(nodes, n, s).doc, true
		}
	}
	return model.AWSPolicyDocument{}, false
}

func awsParseSpec(nodes []*model.Node, n *model.Node, s awsDocSpec) *awsParsed {
	if s.body == nil {
		return awsParseDocument(n, s)
	}
	return awsParseAttr(nodes, s, GetAttr(s.body, s.attr))
}

// AWSWrite writes doc (matched by Path) into n. Untouched statements keep
// their source; read-only documents and statements are refused.
func AWSWrite(nodes []*model.Node, n *model.Node, doc model.AWSPolicyDocument) error {
	var spec *awsDocSpec
	for _, s := range awsSpecs(n) {
		if s.path == doc.Path {
			spec = &s
			break
		}
	}
	if spec == nil {
		return Editf("access: %s has no policy document %q", Address(n), doc.Path)
	}
	p := awsParseSpec(nodes, n, *spec)
	if !p.doc.Editable {
		return Editf("access: the %s of %s can't be edited visually: %s", strings.ToLower(p.doc.Label), Address(n), p.doc.Reason)
	}
	idx, err := awsMatch(p, doc.Statements)
	if err != nil {
		return err
	}
	if awsUnchanged(p, doc, idx) {
		return nil
	}
	switch p.doc.Encoding {
	case "document":
		return awsWriteDocument(n, p, doc, idx)
	case "json":
		src, err := awsWriteJSON(p, doc, idx)
		if err != nil {
			return err
		}
		SetExpr(spec.body, spec.attr, src)
	default:
		src, err := awsWriteJSONEncode(p, doc, idx)
		if err != nil {
			return err
		}
		SetExpr(spec.body, spec.attr, src)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Parsed documents

// awsParsed is a document with what writing it back needs.
type awsParsed struct {
	doc   model.AWSPolicyDocument
	stmts []awsStmt // parallel to doc.Statements
	// fresh: the attribute is not set yet (written as a new jsonencode).
	fresh bool

	// jsonencode
	src        string
	obj        *hclsyntax.ObjectConsExpr
	stmtVal    hclsyntax.Expression
	versionVal hclsyntax.Expression

	// json
	js *awsJSONDoc

	// single: Statement is one object, not a list.
	single bool
}

// awsStmt is one parsed statement.
type awsStmt struct {
	st model.AWSStatement
	// keys are the statement's keys in source order (jsonencode, json).
	keys []awsKey
	// block is the statement block (document).
	block *model.Block
	// raw is the statement's JSON text with interpolation placeholders (json).
	raw string
}

// awsKey is one key of a statement: name as written (unquoted) and value
// source (HCL for jsonencode, placeholder JSON for json).
type awsKey struct{ name, value string }

func (p *awsParsed) add(s awsStmt) {
	p.stmts = append(p.stmts, s)
	p.doc.Statements = append(p.doc.Statements, s.st)
}

func (p *awsParsed) readOnly(encoding, reason string) *awsParsed {
	p.doc.Encoding = encoding
	p.doc.Editable = false
	p.doc.Reason = reason
	return p
}

var awsRefRe = regexp.MustCompile(`^data\.aws_iam_policy_document\.([A-Za-z_][A-Za-z0-9_-]*)\.(json|minified_json)$`)

func awsParseAttr(nodes []*model.Node, s awsDocSpec, a *model.Attr) *awsParsed {
	p := &awsParsed{doc: model.AWSPolicyDocument{Path: s.path, Label: s.label, Statements: []model.AWSStatement{}}}
	src := ""
	if a != nil {
		src = strings.TrimSpace(Source(a.Value))
	}
	if src == "" {
		p.fresh = true
		p.doc.Encoding = "jsonencode"
		p.doc.Editable = true
		return p
	}
	p.src = src
	p.doc.Source = src
	if m := awsRefRe.FindStringSubmatch(src); m != nil {
		addr := "data.aws_iam_policy_document." + m[1]
		for _, d := range nodes {
			if Address(d) == addr {
				p.doc.Linked = d.ID
				linked := awsParseDocument(d, awsDocSpec{label: "Policy document"})
				for _, st := range linked.doc.Statements {
					st.Editable = false
					if st.Reason == "" {
						st.Reason = "Edit it in " + addr + "."
					}
					p.doc.Statements = append(p.doc.Statements, st)
				}
				p.doc.Version = linked.doc.Version
				return p.readOnly("reference", "Built from "+addr+": edit its statements there.")
			}
		}
		return p.readOnly("unsupported", addr+" is not in this workflow.")
	}
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "policy", hcl.InitialPos)
	if diags.HasErrors() {
		return p.readOnly("unsupported", "The policy is not a valid expression.")
	}
	switch x := e.(type) {
	case *hclsyntax.FunctionCallExpr:
		if x.Name == "jsonencode" {
			return awsParseJSONEncode(p, x)
		}
	case *hclsyntax.TemplateExpr:
		return awsParseJSON(p, x)
	}
	return p.readOnly("unsupported", "Nimbo can only show jsonencode({...}), JSON text and aws_iam_policy_document references visually.")
}

// ---------------------------------------------------------------------------
// Matching edits with parsed statements

// awsMatch pairs every edited statement with the parsed statement it
// edits (by Source; -1 = new). Changing or deleting a read-only statement
// is refused.
func awsMatch(p *awsParsed, edit []model.AWSStatement) ([]int, error) {
	used := make([]bool, len(p.stmts))
	idx := make([]int, len(edit))
	for i, st := range edit {
		idx[i] = -1
		if st.Source == "" {
			continue
		}
		best := -1
		for j := range p.stmts {
			if used[j] || p.stmts[j].st.Source != st.Source {
				continue
			}
			if best < 0 {
				best = j
			}
			if awsSameStmt(st, p.stmts[j].st) {
				best = j
				break
			}
		}
		if best < 0 {
			return nil, Editf("access: statement %d changed since the policy was read; reload and try again", i+1)
		}
		used[best] = true
		idx[i] = best
		if o := p.stmts[best].st; !o.Editable && !awsSameStmt(st, o) {
			return nil, Editf("access: statement %d is read-only (%s); edit it in code", i+1, o.Reason)
		}
	}
	for j, u := range used {
		if !u && !p.stmts[j].st.Editable {
			return nil, Editf("access: statement %d is read-only (%s) and can't be deleted here; edit it in code", j+1, p.stmts[j].st.Reason)
		}
	}
	return idx, nil
}

func awsUnchanged(p *awsParsed, doc model.AWSPolicyDocument, idx []int) bool {
	if len(idx) != len(p.stmts) || (doc.Version != "" && doc.Version != p.doc.Version) {
		return false
	}
	for i, j := range idx {
		if j != i || !awsSameStmt(doc.Statements[i], p.stmts[j].st) {
			return false
		}
	}
	return true
}

func awsSameStmt(a, b model.AWSStatement) bool {
	return a.Sid == b.Sid && a.Effect == b.Effect && slices.Equal(a.Actions, b.Actions) &&
		awsSameExprs(a.Resources, b.Resources) && awsSamePrincipals(a.Principals, b.Principals) && awsSameExtras(a.Extra, b.Extra)
}

func awsSameExprs(a, b []string) bool {
	return slices.EqualFunc(a, b, func(x, y string) bool { return strings.TrimSpace(x) == strings.TrimSpace(y) })
}

func awsSamePrincipals(a, b []model.AWSPrincipal) bool {
	return slices.EqualFunc(a, b, func(x, y model.AWSPrincipal) bool { return x.Type == y.Type && awsSameExprs(x.Values, y.Values) })
}

func awsSameExtras(a, b []model.AccessExtra) bool {
	return slices.EqualFunc(a, b, func(x, y model.AccessExtra) bool {
		return x.Key == y.Key && strings.TrimSpace(x.Source) == strings.TrimSpace(y.Source)
	})
}

// awsValidate checks the HCL expressions of a statement.
func awsValidate(i int, st model.AWSStatement) error {
	check := func(what, src string) error {
		if strings.TrimSpace(src) == "" {
			return Editf("access: statement %d has an empty %s", i+1, what)
		}
		if _, diags := hclsyntax.ParseExpression([]byte(src+"\n"), what, hcl.InitialPos); diags.HasErrors() {
			return Editf("access: statement %d: %s %q is not a valid expression", i+1, what, src)
		}
		return nil
	}
	if st.Effect != "Allow" && st.Effect != "Deny" {
		return Editf("access: statement %d: effect must be Allow or Deny, not %q", i+1, st.Effect)
	}
	for _, a := range st.Actions {
		if strings.TrimSpace(a) == "" || strings.ContainsAny(a, " \t\n\"") {
			return Editf("access: statement %d: invalid action %q", i+1, a)
		}
	}
	for _, r := range st.Resources {
		if err := check("resource", r); err != nil {
			return err
		}
	}
	for _, pr := range st.Principals {
		if pr.Type == "" {
			return Editf("access: statement %d has a principal without a type", i+1)
		}
		for _, v := range pr.Values {
			if err := check("principal", v); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// HCL helpers

func awsSlice(src string, r hcl.Range) string {
	if r.Start.Byte < 0 || r.End.Byte > len(src) || r.Start.Byte > r.End.Byte {
		return ""
	}
	return src[r.Start.Byte:r.End.Byte]
}

// awsObjKey is the literal key of an object item ("" when computed).
func awsObjKey(e hclsyntax.Expression) string {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		if k.ForceNonLiteral {
			return ""
		}
		if kw := hcl.ExprAsKeyword(k.Wrapped); kw != "" {
			return kw
		}
		e = k.Wrapped
	}
	if s, ok := awsConstString(e); ok {
		return s
	}
	return ""
}

func awsConstString(e hclsyntax.Expression) (string, bool) {
	if len(e.Variables()) > 0 {
		return "", false
	}
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() || !v.Type().Equals(cty.String) {
		return "", false
	}
	return v.AsString(), true
}

func awsConstStringSrc(src string) (string, bool) {
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "value", hcl.InitialPos)
	if diags.HasErrors() {
		return "", false
	}
	return awsConstString(e)
}

// awsConstStrings evaluates a constant string or tuple of strings.
func awsConstStrings(e hclsyntax.Expression) ([]string, bool) {
	if s, ok := awsConstString(e); ok {
		return []string{s}, true
	}
	t, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, false
	}
	out := []string{}
	for _, x := range t.Exprs {
		s, ok := awsConstString(x)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// awsExprList splits a tuple constructor into its element sources; any
// other expression is one element.
func awsExprList(src string, e hclsyntax.Expression) []string {
	if t, ok := e.(*hclsyntax.TupleConsExpr); ok {
		out := []string{}
		for _, x := range t.Exprs {
			out = append(out, strings.TrimSpace(awsSlice(src, x.Range())))
		}
		return out
	}
	return []string{strings.TrimSpace(awsSlice(src, e.Range()))}
}

// awsDynamic reports for expressions and conditionals inside e.
func awsDynamic(e hclsyntax.Expression) bool {
	found := false
	hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch n.(type) {
		case *hclsyntax.ForExpr, *hclsyntax.ConditionalExpr, *hclsyntax.SplatExpr:
			found = true
		}
		return nil
	})
	return found
}

// awsQuote quotes s as an HCL string literal (no interpolation).
func awsQuote(s string) string { return `"` + awsEscape(s, true) + `"` }

// awsEscape escapes literal text for an HCL quoted string (quoted) or a
// heredoc: template sequences "${" and "%{" are doubled.
func awsEscape(s string, quoted bool) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case (c == '$' || c == '%') && i+1 < len(s) && s[i+1] == '{':
			b.WriteByte(c)
			b.WriteByte(c)
		case quoted && c == '\\':
			b.WriteString(`\\`)
		case quoted && c == '"':
			b.WriteString(`\"`)
		case quoted && c == '\n':
			b.WriteString(`\n`)
		case quoted && c == '\r':
			b.WriteString(`\r`)
		case quoted && c == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// awsLineIndent is the leading whitespace of the line holding offset off.
func awsLineIndent(s string, off int) string {
	if off > len(s) {
		off = len(s)
	}
	start := strings.LastIndexByte(s[:off], '\n') + 1
	end := start
	for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
		end++
	}
	return s[start:end]
}

// awsRenderEntries renders key/value pairs as an HCL object body or JSON
// object, one per line, at indentation ind.
func awsRenderEntries(entries []awsKey, ind string, json bool) string {
	if len(entries) == 0 {
		return "{}"
	}
	width := 0
	for _, e := range entries {
		if !strings.Contains(e.value, "\n") {
			width = max(width, len(e.name))
		}
	}
	var b strings.Builder
	b.WriteString("{\n")
	for i, e := range entries {
		b.WriteString(ind + "  ")
		if json {
			b.WriteString(e.name + ": " + e.value)
			if i < len(entries)-1 {
				b.WriteString(",")
			}
		} else {
			pad := ""
			if !strings.Contains(e.value, "\n") {
				pad = strings.Repeat(" ", max(0, width-len(e.name)))
			}
			b.WriteString(e.name + pad + " = " + e.value)
		}
		b.WriteString("\n")
	}
	b.WriteString(ind + "}")
	return b.String()
}

// awsRenderList renders statement elements as a list at indentation ind.
func awsRenderList(parts []string, ind string) string {
	if len(parts) == 0 {
		return "[]"
	}
	elem := ind + "  "
	return "[\n" + elem + strings.Join(parts, ",\n"+elem) + "\n" + ind + "]"
}

// awsOrderedFields merges the fields of st into the original key order of
// orig: unchanged fields keep their source, removed ones disappear, new
// ones follow (Sid first). value renders a changed field; "" drops it.
func awsOrderedFields(st model.AWSStatement, orig *awsStmt, value func(field string) string, extraValue func(x model.AccessExtra) (string, error), keyName func(string) string) ([]awsKey, error) {
	core := []string{"Sid", "Effect", "Principal", "Action", "Resource"}
	var out []awsKey
	seen := map[string]bool{}
	extras := map[string]model.AccessExtra{}
	for _, x := range st.Extra {
		extras[x.Key] = x
	}
	if orig != nil {
		for _, k := range orig.keys {
			seen[k.name] = true
			if slices.Contains(core, k.name) {
				if awsFieldSame(k.name, st, orig.st) {
					out = append(out, awsKey{keyName(k.name), k.value})
				} else if v := value(k.name); v != "" {
					out = append(out, awsKey{keyName(k.name), v})
				}
				continue
			}
			x, ok := extras[k.name]
			if !ok {
				continue
			}
			if ox, ok := awsExtra(orig.st.Extra, k.name); ok && strings.TrimSpace(ox.Source) == strings.TrimSpace(x.Source) {
				out = append(out, awsKey{keyName(k.name), k.value})
				continue
			}
			v, err := extraValue(x)
			if err != nil {
				return nil, err
			}
			out = append(out, awsKey{keyName(k.name), v})
		}
	}
	for _, f := range core {
		if seen[f] {
			continue
		}
		if v := value(f); v != "" {
			kv := awsKey{keyName(f), v}
			if f == "Sid" {
				out = append([]awsKey{kv}, out...)
			} else {
				out = append(out, kv)
			}
		}
	}
	for _, x := range st.Extra {
		if seen[x.Key] {
			continue
		}
		v, err := extraValue(x)
		if err != nil {
			return nil, err
		}
		out = append(out, awsKey{keyName(x.Key), v})
	}
	return out, nil
}

func awsExtra(xs []model.AccessExtra, key string) (model.AccessExtra, bool) {
	for _, x := range xs {
		if x.Key == key {
			return x, true
		}
	}
	return model.AccessExtra{}, false
}

func awsFieldSame(field string, a, b model.AWSStatement) bool {
	switch field {
	case "Sid":
		return a.Sid == b.Sid
	case "Effect":
		return a.Effect == b.Effect
	case "Action":
		return slices.Equal(a.Actions, b.Actions)
	case "Resource":
		return awsSameExprs(a.Resources, b.Resources)
	case "Principal":
		return awsSamePrincipals(a.Principals, b.Principals)
	}
	return false
}

// ---------------------------------------------------------------------------
// Related nodes

// awsRefersTo reports whether expression src references the node at addr.
func awsRefersTo(src, addr string) bool {
	if !strings.Contains(src, addr) {
		return false
	}
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "value", hcl.InitialPos)
	if diags.HasErrors() {
		return false
	}
	for _, tr := range e.Variables() {
		parts := []string{tr.RootName()}
		for _, step := range tr[1:] {
			a, ok := step.(hcl.TraverseAttr)
			if !ok {
				break
			}
			parts = append(parts, a.Name)
		}
		if joined := strings.Join(parts, "."); joined == addr || strings.HasPrefix(joined, addr+".") {
			return true
		}
	}
	return false
}

// AWSRefersTo reports whether attribute name of n references target.
func AWSRefersTo(n *model.Node, name string, target *model.Node) bool {
	a := GetAttr(&n.Body, name)
	return a != nil && awsRefersTo(Source(a.Value), Address(target))
}

func awsRelated(nodes []*model.Node, n *model.Node) []model.AccessRelated {
	var out []model.AccessRelated
	add := func(m *model.Node, label string) {
		for _, r := range out {
			if r.NodeID == m.ID {
				return
			}
		}
		out = append(out, model.AccessRelated{NodeID: m.ID, Address: Address(m), Label: label})
	}
	holders := map[string]struct{ attr, label string }{
		"aws_iam_role_policy":  {"role", "Role"},
		"aws_iam_user_policy":  {"user", "User"},
		"aws_iam_group_policy": {"group", "Group"},
		"aws_s3_bucket_policy": {"bucket", "Bucket"},
		"aws_sqs_queue_policy": {"queue_url", "Queue"},
		"aws_sns_topic_policy": {"arn", "Topic"},
	}
	switch {
	case n.Kind == model.KindResource && n.Type == "aws_iam_role":
		for _, m := range nodes {
			if m.Kind != model.KindResource {
				continue
			}
			switch m.Type {
			case "aws_iam_role_policy":
				if AWSRefersTo(m, "role", n) {
					add(m, "Inline policy")
				}
			case "aws_iam_role_policy_attachment":
				if !AWSRefersTo(m, "role", n) {
					continue
				}
				add(m, "Managed policy attachment")
				for _, pol := range nodes {
					if pol.Kind == model.KindResource && pol.Type == "aws_iam_policy" && AWSRefersTo(m, "policy_arn", pol) {
						add(pol, "Managed policy")
					}
				}
			}
		}
	case n.Kind == model.KindData:
		for _, m := range nodes {
			if m == n {
				continue
			}
			for _, a := range m.Body.Attrs {
				if awsRefersTo(Source(a.Value), Address(n)) {
					add(m, "Used by")
					break
				}
			}
		}
	default:
		if h, ok := holders[n.Type]; ok {
			for _, m := range nodes {
				if m != n && m.Kind == model.KindResource && AWSRefersTo(n, h.attr, m) {
					add(m, h.label)
				}
			}
		}
		if a := GetAttr(&n.Body, "policy"); a != nil {
			if mm := awsRefRe.FindStringSubmatch(strings.TrimSpace(Source(a.Value))); mm != nil {
				for _, m := range nodes {
					if Address(m) == "data.aws_iam_policy_document."+mm[1] {
						add(m, "Policy document")
					}
				}
			}
		}
		if n.Type == "aws_iam_policy" {
			for _, m := range nodes {
				if m.Kind == model.KindResource && m.Type == "aws_iam_role_policy_attachment" && AWSRefersTo(m, "policy_arn", n) {
					add(m, "Attachment")
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Warnings

// AWSStatementPath is the warning path of statement i of the document at
// docPath ("policy/statements/0"; "statements/0" for a policy document).
func AWSStatementPath(docPath string, i int) string {
	if docPath == "" {
		return "statements/" + strconv.Itoa(i)
	}
	return docPath + "/statements/" + strconv.Itoa(i)
}

// AWSWarnings are the least-privilege warnings of a document's Allow
// statements: administrator access, wildcard actions, "*" resources for
// actions that support resource-level permissions, public principals
// without a condition. Referenced documents warn on their own node.
func AWSWarnings(d model.AWSPolicyDocument) []model.AccessWarning {
	if d.Encoding == "reference" {
		return nil
	}
	var out []model.AccessWarning
	for i, st := range d.Statements {
		if st.Effect == "Deny" {
			continue
		}
		name := fmt.Sprintf("Statement %d", i+1)
		if st.Sid != "" {
			name = fmt.Sprintf("Statement %q", st.Sid)
		}
		path := AWSStatementPath(d.Path, i)
		starRes := slices.ContainsFunc(st.Resources, func(r string) bool { return strings.TrimSpace(r) == `"*"` })
		starAct := slices.ContainsFunc(st.Actions, func(a string) bool { return a == "*" || a == "*:*" })
		warn := func(code, msg, fix string) {
			out = append(out, model.AccessWarning{Code: code, Severity: "warning", Message: msg, Fix: fix, Path: path})
		}
		switch {
		case starAct && starRes:
			warn("aws_admin", name+" allows every action on every resource: full administrator access.",
				"Delete it and grant only what the workload uses: drag the identity onto each resource it needs.")
		default:
			var wild []string
			for _, a := range st.Actions {
				if a == "*" || a == "*:*" || strings.HasSuffix(a, ":*") {
					wild = append(wild, a)
				}
			}
			if len(wild) > 0 {
				warn("aws_wildcard_action", fmt.Sprintf("%s allows every action of a service (%s).", name, strings.Join(wild, ", ")),
					"List only the actions the code calls, e.g. dynamodb:GetItem instead of dynamodb:*.")
			}
			if starRes {
				var scoped []string
				for _, a := range st.Actions {
					if def, ok := AWSAction(a); ok && len(def.Resources) > 0 {
						scoped = append(scoped, a)
					}
				}
				if len(scoped) > 0 {
					warn("aws_wildcard_resource", fmt.Sprintf("%s applies %s to every resource (\"*\").", name, awsShortList(scoped, 3)),
						"These actions can be scoped: link Resource to the resource's ARN (Link to…).")
				}
			}
		}
		if awsPublic(st) {
			warn("aws_public_principal", name+" lets anyone (Principal \"*\") use it, with no condition.",
				"Name the account, role or service that needs access, or add a Condition (e.g. aws:SourceArn) in code.")
		}
	}
	return out
}

func awsPublic(st model.AWSStatement) bool {
	for _, x := range st.Extra {
		if strings.EqualFold(x.Key, "condition") {
			return false
		}
	}
	for _, p := range st.Principals {
		if p.Type == "*" {
			return true
		}
		if p.Type == "AWS" && slices.ContainsFunc(p.Values, func(v string) bool { return strings.TrimSpace(v) == `"*"` }) {
			return true
		}
	}
	return false
}

func awsShortList(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(xs[:n], ", "), len(xs)-n)
}
