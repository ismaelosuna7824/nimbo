package access

import (
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// jsonencode({...}) documents: the object literal is walked as HCL syntax
// and written back by splicing the Statement (and Version) values into the
// original source.

func awsParseJSONEncode(p *awsParsed, fc *hclsyntax.FunctionCallExpr) *awsParsed {
	obj, ok := (*hclsyntax.ObjectConsExpr)(nil), false
	if len(fc.Args) == 1 && !fc.ExpandFinal {
		obj, ok = fc.Args[0].(*hclsyntax.ObjectConsExpr)
	}
	if !ok {
		return p.readOnly("unsupported", "The policy encodes a computed value, not an object literal.")
	}
	p.doc.Encoding = "jsonencode"
	p.doc.Editable = true
	p.obj = obj
	for _, item := range obj.Items {
		switch awsObjKey(item.KeyExpr) {
		case "Version":
			if s, ok := awsConstString(item.ValueExpr); ok {
				p.doc.Version = s
				p.versionVal = item.ValueExpr
			}
		case "Statement":
			p.stmtVal = item.ValueExpr
			switch v := item.ValueExpr.(type) {
			case *hclsyntax.TupleConsExpr:
				for _, e := range v.Exprs {
					p.add(awsParseHCLStmt(p.src, e))
				}
			case *hclsyntax.ObjectConsExpr:
				p.single = true
				p.add(awsParseHCLStmt(p.src, v))
			default:
				return p.readOnly("jsonencode", "Statement is computed ("+strings.TrimSpace(awsSlice(p.src, v.Range()))+"), not a list of statements.")
			}
		}
	}
	return p
}

func awsParseHCLStmt(src string, e hclsyntax.Expression) awsStmt {
	s := awsStmt{st: model.AWSStatement{Editable: true, Source: awsSlice(src, e.Range()), Actions: []string{}, Resources: []string{}}}
	ro := func(reason string) {
		s.st.Editable = false
		if s.st.Reason == "" {
			s.st.Reason = reason
		}
	}
	obj, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok {
		ro("This statement is computed, not an object literal.")
		return s
	}
	if awsDynamic(e) {
		ro("This statement uses a for expression or a conditional.")
	}
	for _, item := range obj.Items {
		key := awsObjKey(item.KeyExpr)
		val := awsSlice(src, item.ValueExpr.Range())
		if key == "" {
			ro("A key of this statement is computed.")
			key = strings.TrimSpace(awsSlice(src, item.KeyExpr.Range()))
			s.keys = append(s.keys, awsKey{key, val})
			s.st.Extra = append(s.st.Extra, model.AccessExtra{Key: key, Source: val})
			continue
		}
		s.keys = append(s.keys, awsKey{key, val})
		switch key {
		case "Sid", "Effect":
			str, ok := awsConstString(item.ValueExpr)
			if !ok {
				ro(key + " is computed.")
			}
			if key == "Sid" {
				s.st.Sid = str
			} else {
				s.st.Effect = str
			}
		case "Action":
			ss, ok := awsConstStrings(item.ValueExpr)
			if !ok {
				ro("Action is computed.")
				ss = []string{}
			}
			s.st.Actions = ss
		case "Resource":
			s.st.Resources = awsExprList(src, item.ValueExpr)
		case "Principal":
			ps, ok := awsHCLPrincipals(src, item.ValueExpr)
			if !ok {
				ro("Principal is computed.")
			}
			s.st.Principals = ps
		default:
			s.st.Extra = append(s.st.Extra, model.AccessExtra{Key: key, Source: val})
		}
	}
	return s
}

func awsHCLPrincipals(src string, e hclsyntax.Expression) ([]model.AWSPrincipal, bool) {
	if s, ok := awsConstString(e); ok {
		if s == "*" {
			return []model.AWSPrincipal{{Type: "*", Values: []string{`"*"`}}}, true
		}
		return nil, false
	}
	obj, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil, false
	}
	var out []model.AWSPrincipal
	for _, item := range obj.Items {
		k := awsObjKey(item.KeyExpr)
		if k == "" {
			return nil, false
		}
		out = append(out, model.AWSPrincipal{Type: k, Values: awsExprList(src, item.ValueExpr)})
	}
	return out, true
}

var awsIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func awsHCLKey(k string) string {
	if awsIdentRe.MatchString(k) {
		return k
	}
	return awsQuote(k)
}

func awsHCLList(items []string) string { return "[" + strings.Join(items, ", ") + "]" }

func awsHCLQuoted(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = awsQuote(s)
	}
	return out
}

func awsTrimmed(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.TrimSpace(s)
	}
	return out
}

// awsHCLField renders a changed field of st ("" drops it).
func awsHCLField(field string, st model.AWSStatement) string {
	switch field {
	case "Sid":
		if st.Sid == "" {
			return ""
		}
		return awsQuote(st.Sid)
	case "Effect":
		return awsQuote(st.Effect)
	case "Action":
		if len(st.Actions) == 0 {
			return ""
		}
		return awsHCLList(awsHCLQuoted(st.Actions))
	case "Resource":
		switch len(st.Resources) {
		case 0:
			return ""
		case 1:
			return strings.TrimSpace(st.Resources[0])
		}
		return awsHCLList(awsTrimmed(st.Resources))
	case "Principal":
		if len(st.Principals) == 0 {
			return ""
		}
		if len(st.Principals) == 1 && st.Principals[0].Type == "*" {
			return `"*"`
		}
		items := make([]string, len(st.Principals))
		for i, p := range st.Principals {
			v := awsHCLList(awsTrimmed(p.Values))
			if len(p.Values) == 1 {
				v = strings.TrimSpace(p.Values[0])
			}
			items[i] = awsHCLKey(p.Type) + " = " + v
		}
		return "{ " + strings.Join(items, ", ") + " }"
	}
	return ""
}

// awsHCLParts renders the statements of doc as jsonencode elements at
// indentation ind: unchanged ones are their original source.
func awsHCLParts(p *awsParsed, doc model.AWSPolicyDocument, idx []int, ind string) ([]string, error) {
	parts := make([]string, 0, len(doc.Statements))
	for i, st := range doc.Statements {
		j := idx[i]
		if j >= 0 && awsSameStmt(st, p.stmts[j].st) {
			parts = append(parts, p.stmts[j].st.Source)
			continue
		}
		if err := awsValidate(i, st); err != nil {
			return nil, err
		}
		var orig *awsStmt
		if j >= 0 {
			orig = &p.stmts[j]
		}
		entries, err := awsOrderedFields(st, orig,
			func(f string) string { return awsHCLField(f, st) },
			func(x model.AccessExtra) (string, error) {
				src := strings.TrimSpace(x.Source)
				if _, diags := hclsyntax.ParseExpression([]byte(src+"\n"), x.Key, hcl.InitialPos); diags.HasErrors() || src == "" {
					return "", Editf("access: statement %d: %s is not a valid expression", i+1, x.Key)
				}
				return src, nil
			},
			awsHCLKey)
		if err != nil {
			return nil, err
		}
		parts = append(parts, awsRenderEntries(entries, ind, false))
	}
	return parts, nil
}

type awsRepl struct {
	start, end int
	text       string
}

func awsSplice(src string, rs []awsRepl) string {
	slices.SortFunc(rs, func(a, b awsRepl) int { return b.start - a.start })
	for _, r := range rs {
		src = src[:r.start] + r.text + src[r.end:]
	}
	return src
}

func awsWriteJSONEncode(p *awsParsed, doc model.AWSPolicyDocument, idx []int) (string, error) {
	if p.fresh {
		parts, err := awsHCLParts(p, doc, idx, "    ")
		if err != nil {
			return "", err
		}
		version := doc.Version
		if version == "" {
			version = "2012-10-17"
		}
		return "jsonencode({\n  Version = " + awsQuote(version) + "\n  Statement = " + awsRenderList(parts, "  ") + "\n})", nil
	}
	src := p.src
	var rs []awsRepl
	if p.stmtVal != nil {
		r := p.stmtVal.Range()
		ind := awsLineIndent(src, r.Start.Byte)
		parts, err := awsHCLParts(p, doc, idx, ind+"  ")
		if err != nil {
			return "", err
		}
		val := awsRenderList(parts, ind)
		if p.single && len(parts) == 1 {
			val = parts[0]
		}
		rs = append(rs, awsRepl{r.Start.Byte, r.End.Byte, val})
	} else {
		end := p.obj.SrcRange.End.Byte - 1
		closeInd := awsLineIndent(src, end)
		ind := closeInd + "  "
		parts, err := awsHCLParts(p, doc, idx, ind+"  ")
		if err != nil {
			return "", err
		}
		prefix := ""
		if strings.TrimSpace(src[strings.LastIndexByte(src[:end], '\n')+1:end]) != "" {
			prefix = "\n" + closeInd
		}
		rs = append(rs, awsRepl{end, end, prefix + "  Statement = " + awsRenderList(parts, ind) + "\n" + closeInd})
	}
	if doc.Version != "" && doc.Version != p.doc.Version && p.versionVal != nil {
		r := p.versionVal.Range()
		rs = append(rs, awsRepl{r.Start.Byte, r.End.Byte, awsQuote(doc.Version)})
	}
	return awsSplice(src, rs), nil
}
