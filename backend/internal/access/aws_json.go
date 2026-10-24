package access

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// JSON documents: a quoted string or heredoc holding JSON. Interpolations
// (${aws_s3_bucket.b.arn}) are replaced by placeholders while the JSON is
// parsed and restored when it is written back; a JSON string that is only
// an interpolation becomes that expression (aws_s3_bucket.b.arn), one with
// literal text an HCL template ("${aws_s3_bucket.b.arn}/*").

// awsJSONDoc is a parsed JSON document.
type awsJSONDoc struct {
	heredoc                                bool
	header, delim, closeIndent, bodyIndent string
	strip                                  bool
	// text is the JSON with placeholders for interps.
	text    string
	interps []string
	members []awsJSONMember // top-level keys
	open    int             // offset of the opening brace
	stmt    awsJSONMember   // Statement (start < 0 when absent)
	version awsJSONMember   // Version (start < 0 when absent)
}

// awsJSONMember is an object key and the span of its value in text.
type awsJSONMember struct {
	key        string
	start, end int
}

var awsPHRe = regexp.MustCompile("\uE000([0-9]+)\uE001")

func awsPH(i int) string { return "\uE000" + strconv.Itoa(i) + "\uE001" }

// awsPHParts joins template parts into text with placeholders; directives
// (%{if}, %{for}) are not supported.
func awsPHParts(src string, parts []hclsyntax.Expression, interps *[]string) (string, bool) {
	var b strings.Builder
	for _, part := range parts {
		switch x := part.(type) {
		case *hclsyntax.LiteralValueExpr:
			if x.Val.Type().Equals(cty.String) && x.Val.IsKnown() && !x.Val.IsNull() {
				b.WriteString(x.Val.AsString())
				continue
			}
		case *hclsyntax.ConditionalExpr, *hclsyntax.TemplateJoinExpr, *hclsyntax.ForExpr:
			return "", false
		}
		b.WriteString(awsPH(len(*interps)))
		*interps = append(*interps, strings.TrimSpace(awsSlice(src, part.Range())))
	}
	return b.String(), true
}

// awsTemplateText restores placeholders as ${...} and escapes the literal
// text for a quoted HCL string (quoted) or a heredoc.
func awsTemplateText(ph string, interps []string, quoted bool) string {
	var b strings.Builder
	last := 0
	for _, m := range awsPHRe.FindAllStringSubmatchIndex(ph, -1) {
		b.WriteString(awsEscape(ph[last:m[0]], quoted))
		i, _ := strconv.Atoi(ph[m[2]:m[3]])
		b.WriteString("${" + interps[i] + "}")
		last = m[1]
	}
	b.WriteString(awsEscape(ph[last:], quoted))
	return b.String()
}

// awsExprFromPH is the HCL expression of a JSON string value.
func awsExprFromPH(v string, interps []string) string {
	if m := awsPHRe.FindStringSubmatchIndex(v); m != nil && m[0] == 0 && m[1] == len(v) {
		i, _ := strconv.Atoi(v[m[2]:m[3]])
		return interps[i]
	}
	return `"` + awsTemplateText(v, interps, true) + `"`
}

// awsPHFromExpr is the JSON string value (with placeholders) of an HCL
// expression: a quoted template keeps its text, anything else is one
// interpolation.
func awsPHFromExpr(src string, interps *[]string) string {
	src = strings.TrimSpace(src)
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "value", hcl.InitialPos)
	if !diags.HasErrors() {
		switch t := e.(type) {
		case *hclsyntax.TemplateExpr:
			if !strings.HasPrefix(src, "<<") {
				mark := len(*interps)
				if s, ok := awsPHParts(src, t.Parts, interps); ok {
					return s
				}
				*interps = (*interps)[:mark]
			}
		case *hclsyntax.TemplateWrapExpr:
			src = strings.TrimSpace(awsSlice(src, t.Wrapped.Range()))
		}
	}
	*interps = append(*interps, src)
	return awsPH(len(*interps) - 1)
}

// awsPHFromTemplate turns template text ("{\"StringEquals\": \"${x}\"}"
// as shown in Extra) back into placeholder JSON.
func awsPHFromTemplate(tmpl string, interps *[]string) (string, error) {
	e, diags := hclsyntax.ParseTemplate([]byte(tmpl), "extra", hcl.InitialPos)
	if diags.HasErrors() {
		return "", errors.New(diags.Error())
	}
	switch t := e.(type) {
	case *hclsyntax.TemplateExpr:
		if s, ok := awsPHParts(tmpl, t.Parts, interps); ok {
			return s, nil
		}
		return "", errors.New("template directives are not supported")
	case *hclsyntax.TemplateWrapExpr:
		*interps = append(*interps, strings.TrimSpace(awsSlice(tmpl, t.Wrapped.Range())))
		return awsPH(len(*interps) - 1), nil
	}
	return tmpl, nil
}

func awsJSONString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func awsJSONStrings(raw string) ([]string, bool) {
	var one string
	if json.Unmarshal([]byte(raw), &one) == nil {
		return []string{one}, true
	}
	var many []string
	if json.Unmarshal([]byte(raw), &many) == nil && many != nil {
		return many, true
	}
	return nil, false
}

// awsJSONObject reads the object spanning text[start:end]: its members
// (value spans) and the offsets of its braces.
func awsJSONObject(text string, start, end int) (members []awsJSONMember, open, close int, ok bool) {
	dec := json.NewDecoder(strings.NewReader(text[start:end]))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, 0, 0, false
	}
	open = start + int(dec.InputOffset()) - 1
	for dec.More() {
		kt, err := dec.Token()
		k, isStr := kt.(string)
		if err != nil || !isStr {
			return nil, 0, 0, false
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, 0, 0, false
		}
		e := start + int(dec.InputOffset())
		members = append(members, awsJSONMember{key: k, start: e - len(raw), end: e})
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, 0, 0, false
	}
	close = start + int(dec.InputOffset()) - 1
	if _, err := dec.Token(); err != io.EOF {
		return nil, 0, 0, false
	}
	return members, open, close, true
}

// awsJSONArray returns the element spans of the array text[start:end].
func awsJSONArray(text string, start, end int) ([][2]int, bool) {
	dec := json.NewDecoder(strings.NewReader(text[start:end]))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, false
	}
	var out [][2]int
	for dec.More() {
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, false
		}
		e := start + int(dec.InputOffset())
		out = append(out, [2]int{e - len(raw), e})
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, false
	}
	return out, true
}

func awsParseJSON(p *awsParsed, t *hclsyntax.TemplateExpr) *awsParsed {
	src := p.src
	js := &awsJSONDoc{stmt: awsJSONMember{start: -1}, version: awsJSONMember{start: -1}}
	if strings.HasPrefix(src, "<<") {
		nl := strings.IndexByte(src, '\n')
		lastNL := strings.LastIndexByte(src, '\n')
		if nl < 0 || lastNL <= nl {
			return p.readOnly("unsupported", "The heredoc could not be read.")
		}
		js.heredoc = true
		js.header = strings.TrimRight(src[:nl], " \t\r")
		js.strip = strings.HasPrefix(js.header, "<<-")
		js.delim = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(js.header, "<<-"), "<<"))
		last := src[lastNL+1:]
		js.closeIndent = last[:len(last)-len(strings.TrimLeft(last, " \t"))]
		if js.strip {
			narrowest := -1
			for l := range strings.SplitSeq(src[nl+1:lastNL], "\n") {
				if strings.TrimSpace(l) == "" {
					continue
				}
				w := len(l) - len(strings.TrimLeft(l, " \t"))
				if narrowest < 0 || w < narrowest {
					narrowest = w
					js.bodyIndent = l[:w]
				}
			}
		}
	}
	text, ok := awsPHParts(src, t.Parts, &js.interps)
	if !ok {
		return p.readOnly("unsupported", "The JSON text uses template directives (%{...}).")
	}
	js.text = text
	members, open, _, ok := awsJSONObject(text, 0, len(text))
	if !ok {
		return p.readOnly("unsupported", "The policy text is not a JSON object Nimbo can read.")
	}
	js.members, js.open = members, open
	p.js = js
	p.doc.Encoding = "json"
	p.doc.Editable = true
	for _, m := range members {
		val := text[m.start:m.end]
		switch m.key {
		case "Version":
			var v string
			if json.Unmarshal([]byte(val), &v) == nil {
				p.doc.Version = v
				js.version = m
			}
		case "Statement":
			js.stmt = m
			switch val[0] {
			case '[':
				spans, ok := awsJSONArray(text, m.start, m.end)
				if !ok {
					return p.readOnly("json", "Statement could not be read.")
				}
				for _, sp := range spans {
					p.add(awsParseJSONStmt(js, sp[0], sp[1]))
				}
			case '{':
				p.single = true
				p.add(awsParseJSONStmt(js, m.start, m.end))
			default:
				return p.readOnly("json", "Statement is not a list of statements.")
			}
		}
	}
	return p
}

func awsParseJSONStmt(js *awsJSONDoc, start, end int) awsStmt {
	raw := js.text[start:end]
	s := awsStmt{raw: raw, st: model.AWSStatement{Editable: true, Source: awsTemplateText(raw, js.interps, false), Actions: []string{}, Resources: []string{}}}
	ro := func(reason string) {
		s.st.Editable = false
		if s.st.Reason == "" {
			s.st.Reason = reason
		}
	}
	members, _, _, ok := awsJSONObject(js.text, start, end)
	if !ok {
		ro("This statement is not a JSON object.")
		return s
	}
	for _, m := range members {
		val := js.text[m.start:m.end]
		s.keys = append(s.keys, awsKey{m.key, val})
		switch m.key {
		case "Sid", "Effect":
			var str string
			if json.Unmarshal([]byte(val), &str) != nil || awsPHRe.MatchString(str) {
				ro(m.key + " is not plain text.")
			}
			if m.key == "Sid" {
				s.st.Sid = str
			} else {
				s.st.Effect = str
			}
		case "Action":
			ss, ok := awsJSONStrings(val)
			if !ok || slices.ContainsFunc(ss, awsPHRe.MatchString) {
				ro("Action is not a list of action names.")
				ss = []string{}
			}
			s.st.Actions = ss
		case "Resource":
			ss, ok := awsJSONStrings(val)
			if !ok {
				ro("Resource is not a string or a list of strings.")
			}
			for _, r := range ss {
				s.st.Resources = append(s.st.Resources, awsExprFromPH(r, js.interps))
			}
		case "Principal":
			ps, ok := awsJSONPrincipals(val, js.interps)
			if !ok {
				ro("Principal could not be read.")
			}
			s.st.Principals = ps
		default:
			s.st.Extra = append(s.st.Extra, model.AccessExtra{Key: m.key, Source: awsTemplateText(val, js.interps, false)})
		}
	}
	return s
}

func awsJSONPrincipals(raw string, interps []string) ([]model.AWSPrincipal, bool) {
	var star string
	if json.Unmarshal([]byte(raw), &star) == nil {
		return []model.AWSPrincipal{{Type: "*", Values: []string{`"*"`}}}, star == "*"
	}
	members, _, _, ok := awsJSONObject(raw, 0, len(raw))
	if !ok {
		return nil, false
	}
	var out []model.AWSPrincipal
	for _, m := range members {
		ss, ok := awsJSONStrings(raw[m.start:m.end])
		if !ok {
			return nil, false
		}
		p := model.AWSPrincipal{Type: m.key, Values: []string{}}
		for _, v := range ss {
			p.Values = append(p.Values, awsExprFromPH(v, interps))
		}
		out = append(out, p)
	}
	return out, true
}

func awsJSONList(items []string) string { return "[" + strings.Join(items, ", ") + "]" }

// awsJSONField renders a changed field of st as JSON ("" drops it).
func awsJSONField(field string, st model.AWSStatement, interps *[]string) string {
	str := func(expr string) string { return awsJSONString(awsPHFromExpr(expr, interps)) }
	switch field {
	case "Sid":
		if st.Sid == "" {
			return ""
		}
		return awsJSONString(st.Sid)
	case "Effect":
		return awsJSONString(st.Effect)
	case "Action":
		if len(st.Actions) == 0 {
			return ""
		}
		items := make([]string, len(st.Actions))
		for i, a := range st.Actions {
			items[i] = awsJSONString(a)
		}
		return awsJSONList(items)
	case "Resource":
		switch len(st.Resources) {
		case 0:
			return ""
		case 1:
			return str(st.Resources[0])
		}
		items := make([]string, len(st.Resources))
		for i, r := range st.Resources {
			items[i] = str(r)
		}
		return awsJSONList(items)
	case "Principal":
		if len(st.Principals) == 0 {
			return ""
		}
		if len(st.Principals) == 1 && st.Principals[0].Type == "*" {
			return `"*"`
		}
		items := make([]string, len(st.Principals))
		for i, p := range st.Principals {
			v := ""
			if len(p.Values) == 1 {
				v = str(p.Values[0])
			} else {
				vs := make([]string, len(p.Values))
				for k, x := range p.Values {
					vs[k] = str(x)
				}
				v = awsJSONList(vs)
			}
			items[i] = awsJSONString(p.Type) + ": " + v
		}
		return "{" + strings.Join(items, ", ") + "}"
	}
	return ""
}

func awsJSONParts(p *awsParsed, doc model.AWSPolicyDocument, idx []int, ind string, interps *[]string) ([]string, error) {
	parts := make([]string, 0, len(doc.Statements))
	for i, st := range doc.Statements {
		j := idx[i]
		if j >= 0 && awsSameStmt(st, p.stmts[j].st) {
			parts = append(parts, p.stmts[j].raw)
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
			func(f string) string { return awsJSONField(f, st, interps) },
			func(x model.AccessExtra) (string, error) {
				ph, err := awsPHFromTemplate(x.Source, interps)
				if err != nil || !json.Valid([]byte(ph)) {
					return "", Editf("access: statement %d: %s is not valid JSON", i+1, x.Key)
				}
				return strings.TrimSpace(ph), nil
			},
			awsJSONString)
		if err != nil {
			return nil, err
		}
		parts = append(parts, awsRenderEntries(entries, ind, true))
	}
	return parts, nil
}

func awsWriteJSON(p *awsParsed, doc model.AWSPolicyDocument, idx []int) (string, error) {
	js := p.js
	interps := slices.Clone(js.interps)
	text := js.text
	var rs []awsRepl
	if js.stmt.start >= 0 {
		ind := awsLineIndent(text, js.stmt.start)
		parts, err := awsJSONParts(p, doc, idx, ind+"  ", &interps)
		if err != nil {
			return "", err
		}
		val := awsRenderList(parts, ind)
		if p.single && len(parts) == 1 {
			val = parts[0]
		}
		rs = append(rs, awsRepl{js.stmt.start, js.stmt.end, val})
	} else if len(js.members) > 0 {
		last := js.members[len(js.members)-1]
		ind := awsLineIndent(text, last.start)
		parts, err := awsJSONParts(p, doc, idx, ind+"  ", &interps)
		if err != nil {
			return "", err
		}
		rs = append(rs, awsRepl{last.end, last.end, ",\n" + ind + `"Statement": ` + awsRenderList(parts, ind)})
	} else {
		parts, err := awsJSONParts(p, doc, idx, "    ", &interps)
		if err != nil {
			return "", err
		}
		rs = append(rs, awsRepl{js.open + 1, js.open + 1, "\n  \"Statement\": " + awsRenderList(parts, "  ") + "\n"})
	}
	if doc.Version != "" && doc.Version != p.doc.Version && js.version.start >= 0 {
		rs = append(rs, awsRepl{js.version.start, js.version.end, awsJSONString(doc.Version)})
	}
	text = awsSplice(text, rs)
	if !js.heredoc {
		return `"` + awsTemplateText(text, interps, true) + `"`, nil
	}
	body := strings.TrimSuffix(awsTemplateText(text, interps, false), "\n")
	lines := strings.Split(body, "\n")
	if js.strip {
		for i, l := range lines {
			if strings.TrimSpace(l) != "" {
				lines[i] = js.bodyIndent + l
			}
		}
	}
	return js.header + "\n" + strings.Join(lines, "\n") + "\n" + js.closeIndent + js.delim, nil
}
