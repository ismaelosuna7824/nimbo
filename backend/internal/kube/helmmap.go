package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
)

// The Helm source map: which values key renders each field. The chart is
// rendered a second time with every string value replaced by a unique
// token ("nmb12z") and every integer by a unique number (9100012); a
// field of the second render holding tokens comes from those keys, with
// the literal text around them (an image "{{ .Values.image.repository }}:
// {{ .Values.image.tag }}" renders "nmb3z:nmb4z"). Values whose token
// makes the chart fail (validated enums) keep their value: the failing
// keys are found by bisection. Empty strings and booleans keep their
// value (they steer conditionals).

// helmMap is a Helm source's source map.
type helmMap struct {
	err     error
	objs    map[string]*Object // sentinel render objects by matchKey
	tokens  map[string]string  // token -> values key
	numbers map[int]string     // number -> values key
	current map[string]string  // values key -> current value
	// literals maps matchKey|field keys to the template line writing
	// the field literally (see literalMap).
	literals map[string]litRef
	// seq are the marked render's objects of every template, in order.
	seq map[string][]*Object
}

var tokenRe = regexp.MustCompile(`(?i)nmb\d+z`)

const numberBase = 9100000

// valuesPattern is a field rendered from values keys with literal text
// around them: parts[0] keys[0] parts[1] ... keys[n-1] parts[n].
type valuesPattern struct {
	keys  []string
	parts []string
}

// decomposable reports whether a new value can be split back into the
// keys: adjacent keys need a literal between them.
func (vp *valuesPattern) decomposable() bool {
	for i := 1; i < len(vp.parts)-1; i++ {
		if vp.parts[i] == "" {
			return false
		}
	}
	return true
}

// split decomposes a new field value into the keys' values, preferring
// the split that keeps the most current values (cur), greedy first.
func (vp *valuesPattern) split(value string, cur []string) ([]string, bool) {
	n := len(vp.keys)
	if n == 1 {
		v, ok := strings.CutPrefix(value, vp.parts[0])
		if !ok {
			return nil, false
		}
		v, ok = strings.CutSuffix(v, vp.parts[1])
		return []string{v}, ok
	}
	best, bestScore := []string(nil), -1
	for mask := range 1 << n {
		var b strings.Builder
		b.WriteString("^" + regexp.QuoteMeta(vp.parts[0]))
		for i := range n {
			if mask&(1<<i) != 0 {
				b.WriteString("(.*?)")
			} else {
				b.WriteString("(.*)")
			}
			b.WriteString(regexp.QuoteMeta(vp.parts[i+1]))
		}
		b.WriteString("$")
		m := regexp.MustCompile(b.String()).FindStringSubmatch(value)
		if m == nil {
			continue
		}
		score := 0
		for i := range n {
			if i < len(cur) && m[i+1] == cur[i] {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = m[1:], score
		}
	}
	return best, best != nil
}

// pattern parses a sentinel-rendered field (nil without tokens). Number
// sentinels inside the text get their current values back (they stay
// literal parts).
func (h *helmMap) pattern(text string) *valuesPattern {
	locs := tokenRe.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return nil
	}
	vp := &valuesPattern{}
	prev := 0
	for _, l := range locs {
		key, ok := h.tokens[strings.ToLower(text[l[0]:l[1]])]
		if !ok {
			return nil
		}
		vp.parts = append(vp.parts, h.unnumber(text[prev:l[0]]))
		vp.keys = append(vp.keys, key)
		prev = l[1]
	}
	vp.parts = append(vp.parts, h.unnumber(text[prev:]))
	return vp
}

var numberRe = regexp.MustCompile(`91\d{5}`)

// unnumber puts the current values back in place of number sentinels.
func (h *helmMap) unnumber(s string) string {
	return numberRe.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(m)
		if key, ok := h.numbers[n]; ok {
			return h.current[key]
		}
		return m
	})
}

// render is the field the pattern gives for the keys' values.
func (vp *valuesPattern) render(values map[string]string) string {
	var b strings.Builder
	for i, k := range vp.keys {
		b.WriteString(vp.parts[i] + values[k])
	}
	b.WriteString(vp.parts[len(vp.parts)-1])
	return b.String()
}

// matchKey identifies an object across renders: its template, kind and
// ordinal among the template's objects of that kind.
func matchKey(template, kind string, n int) string {
	return template + "|" + kind + "|" + strconv.Itoa(n)
}

// match finds o in the marked render: same template, kind and ordinal, or
// (when a value names the kind) the same position in the template.
func (h *helmMap) match(o *Object) *Object {
	if h.objs == nil {
		return nil
	}
	if m := h.objs[o.matchKey]; m != nil {
		return m
	}
	if seq := h.seq[o.Template]; o.tplIndex < len(seq) && tokenRe.MatchString(seq[o.tplIndex].Kind) {
		return seq[o.tplIndex]
	}
	return nil
}

// keySet is a set of values keys. A compared key steers only itself (a
// map passed to hasKey does not make its entries steer), or the items of
// a list it is (`has "x" .Values.list`).
type keySet map[string]bool

func (s keySet) covers(key string) bool {
	if s[key] {
		return true
	}
	if i := strings.LastIndexByte(key, '['); i > 0 && strings.HasSuffix(key, "]") {
		return s[key[:i]]
	}
	return false
}

var (
	actionRe     = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	compareRe    = regexp.MustCompile(`\b(eq|ne|lt|le|gt|ge|contains|hasPrefix|hasSuffix|regexMatch|mustRegexMatch|semverCompare|has|mustHas|hasKey)\b`)
	valuesPathRe = regexp.MustCompile(`\.Values((?:\.[A-Za-z_][A-Za-z0-9_\-]*)+)`)
)

// comparedKeys are the values keys the chart's templates (and its
// vendored subcharts', under their name) compare: a token in them would
// switch the template to another branch.
func comparedKeys(h *helmRender) keySet {
	out := keySet{}
	for _, f := range h.files {
		if !strings.Contains(f.Name, "templates/") {
			continue
		}
		prefix := ""
		if rest, ok := strings.CutPrefix(f.Name, "charts/"); ok {
			sub, _, _ := strings.Cut(rest, "/")
			prefix = sub + "."
		}
		for _, a := range actionRe.FindAllString(string(f.Data), -1) {
			if !compareRe.MatchString(a) {
				continue
			}
			for _, m := range valuesPathRe.FindAllStringSubmatch(a, -1) {
				out[prefix+strings.TrimPrefix(m[1], ".")] = true
			}
		}
	}
	return out
}

var (
	helmMaps   = map[string]*helmMap{}
	helmMapsMu sync.Mutex
)

// helmSourceMap computes (or reuses) the source map of a rendered Helm
// source.
func (p *Project) helmSourceMap(r *Rendered) *helmMap {
	h := r.helm
	if h == nil || h.templates == nil {
		return &helmMap{err: fmt.Errorf("the chart did not render")}
	}
	sum := sha256.New()
	for _, f := range h.files {
		sum.Write([]byte(f.Name))
		sum.Write([]byte{0})
		sum.Write(f.Data)
		sum.Write([]byte{0})
	}
	vb, _ := json.Marshal(h.vals)
	sum.Write(vb)
	sum.Write([]byte(h.release + "\x00" + h.namespace))
	key := hex.EncodeToString(sum.Sum(nil))
	helmMapsMu.Lock()
	hm := helmMaps[key]
	helmMapsMu.Unlock()
	if hm != nil {
		return hm
	}
	hm = buildHelmMap(h)
	helmMapsMu.Lock()
	if len(helmMaps) >= 16 {
		for k := range helmMaps {
			delete(helmMaps, k)
			break
		}
	}
	helmMaps[key] = hm
	helmMapsMu.Unlock()
	return hm
}

// valueLeaf is one sentinel-able value.
type valueLeaf struct {
	key   string // values path ("image.tag", "extraEnv[0].value")
	keys  []string
	str   bool
	token string
	num   int
	cur   string // current value
}

func buildHelmMap(h *helmRender) *helmMap {
	c, err := loader.LoadFiles(h.files)
	if err != nil {
		return &helmMap{err: err}
	}
	full, err := util.CoalesceValues(c, h.vals)
	if err != nil {
		return &helmMap{err: err}
	}
	var vls []valueLeaf
	var walk func(v any, key string, keys []string)
	walk = func(v any, key string, keys []string) {
		switch t := v.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(t)) {
				sub := k
				if key != "" {
					sub = key + "." + k
				}
				if strings.ContainsAny(k, ".[]") {
					sub = key + `["` + k + `"]`
				}
				walk(t[k], sub, append(slices.Clone(keys), k))
			}
		case []any:
			for i, it := range t {
				walk(it, key+"["+strconv.Itoa(i)+"]", append(slices.Clone(keys), strconv.Itoa(i)))
			}
		case string:
			if t != "" {
				vls = append(vls, valueLeaf{key: key, keys: keys, str: true, cur: t})
			}
		case int, int64, float64:
			if n, ok := toInt(t); ok && fmt.Sprint(t) == strconv.Itoa(n) {
				vls = append(vls, valueLeaf{key: key, keys: keys, cur: strconv.Itoa(n)})
			}
		}
	}
	walk(map[string]any(full), "", nil)
	hm := &helmMap{tokens: map[string]string{}, numbers: map[int]string{}, current: map[string]string{}}
	for _, v := range vls {
		hm.current[v.key] = v.cur
	}
	for i := range vls {
		if vls[i].str {
			vls[i].token = "nmb" + strconv.Itoa(i) + "z"
		} else {
			vls[i].num = numberBase + i
		}
	}
	render := func(set []int) (map[string]string, error) {
		vals := deepCopy(map[string]any(full)).(map[string]any)
		for _, i := range set {
			var v any = vls[i].token
			if !vls[i].str {
				v = vls[i].num
			}
			setIn(vals, vls[i].keys, v)
		}
		_, tpls, err := helmTemplate(h.files, vals, h.release, h.namespace)
		return tpls, err
	}
	// Values compared in the templates steer them: they keep their value.
	compared := comparedKeys(h)
	all := make([]int, 0, len(vls))
	for i, v := range vls {
		if !v.str || !compared.covers(v.key) {
			all = append(all, i)
		}
	}
	// Accept the marks chunk by chunk; a chunk that breaks the chart is
	// split until the culprits are found (renders are bounded).
	budget := 160
	var used []int
	tpls, err := render(nil)
	if err != nil {
		return &helmMap{err: err}
	}
	var try func(chunk []int)
	try = func(chunk []int) {
		if len(chunk) == 0 || budget <= 0 {
			return
		}
		budget--
		cand := append(slices.Clone(used), chunk...)
		if t, err := render(cand); err == nil {
			used, tpls = cand, t
			return
		}
		if len(chunk) == 1 {
			return
		}
		try(chunk[:len(chunk)/2])
		try(chunk[len(chunk)/2:])
	}
	const chunkSize = 64
	for i := 0; i < len(all); i += chunkSize {
		try(all[i:min(i+chunkSize, len(all))])
	}
	for _, i := range used {
		if vls[i].str {
			hm.tokens[vls[i].token] = vls[i].key
		} else {
			hm.numbers[vls[i].num] = vls[i].key
		}
	}
	hm.objs, hm.seq = map[string]*Object{}, map[string][]*Object{}
	for name, text := range tpls {
		if strings.TrimSpace(text) == "" || !strings.Contains(name, "templates/") {
			continue
		}
		f := ParseFile(name, helmDocs(text))
		objs, _ := objectsOf(f)
		seen := map[string]int{}
		for _, o := range objs {
			hm.objs[matchKey(name, o.Kind, seen[o.Kind])] = o
			seen[o.Kind]++
		}
		hm.seq[name] = objs
	}
	hm.literals = literalMap(h)
	return hm
}

// litRef is a literal "key: value" line of a template.
type litRef struct {
	file    string // workflow file
	line    int
	literal string // the value as written (quotes kept)
}

var (
	litLineRe  = regexp.MustCompile(`^(\s*-?\s*["']?[A-Za-z0-9_.\-/]+["']?:\s+)(["']?)([^"'{}#\s|>&*!%@\x60\[][^"'{}#]*?)(["']?)(\s*(?:#.*)?)$`)
	litTokenRe = regexp.MustCompile(`(?i)^nmbl(\d+)z$`)
)

// literalMap finds the fields written literally in the chart's templates:
// every "key: value" line (no template action) of templates/*.yaml gets a
// unique token, the chart is rendered once more, and a field showing
// exactly one token, which shows nowhere else, comes from that line. It
// maps matchKey + "|" + concrete field keys to the line.
func literalMap(h *helmRender) map[string]litRef {
	var refs []litRef
	files := make([]*archive.BufferedFile, len(h.files))
	for i, f := range h.files {
		files[i] = f
		if !strings.HasSuffix(f.Name, ".yaml") && !strings.HasSuffix(f.Name, ".yml") || !strings.Contains(f.Name, "templates/") ||
			strings.HasPrefix(path.Base(f.Name), "_") {
			continue
		}
		lines := strings.Split(string(f.Data), "\n")
		changed := false
		for j, l := range lines {
			m := litLineRe.FindStringSubmatch(l)
			if m == nil || m[2] != m[4] || strings.Contains(l, "{{") {
				continue
			}
			key := strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m[1]), "-")), `:"' `)
			if key == "apiVersion" || key == "kind" {
				continue
			}
			refs = append(refs, litRef{file: h.workflowFile(f.Name), line: j + 1, literal: m[2] + m[3] + m[4]})
			lines[j] = m[1] + m[2] + "nmbl" + strconv.Itoa(len(refs)-1) + "z" + m[4] + m[5]
			changed = true
		}
		if changed {
			files[i] = &archive.BufferedFile{Name: f.Name, ModTime: f.ModTime, Data: []byte(strings.Join(lines, "\n"))}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	_, tpls, err := helmTemplate(files, h.vals, h.release, h.namespace)
	if err != nil {
		return nil
	}
	seen := map[int]int{}
	found := map[string]int{}
	for name, text := range tpls {
		if strings.TrimSpace(text) == "" || !strings.Contains(name, "templates/") {
			continue
		}
		objs, _ := objectsOf(ParseFile(name, helmDocs(text)))
		kinds := map[string]int{}
		for _, o := range objs {
			mk := matchKey(name, o.Kind, kinds[o.Kind])
			kinds[o.Kind]++
			for _, l := range leaves(o.Obj) {
				s, ok := l.value.(string)
				if !ok {
					continue
				}
				if m := litTokenRe.FindStringSubmatch(s); m != nil {
					n, _ := strconv.Atoi(m[1])
					seen[n]++
					found[mk+"|"+strings.Join(l.keys, "\x00")] = n
				}
			}
		}
	}
	out := map[string]litRef{}
	for k, n := range found {
		if seen[n] == 1 && n < len(refs) {
			out[k] = refs[n]
		}
	}
	return out
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, val := range t {
			l[i] = deepCopy(val)
		}
		return l
	}
	return v
}

func setIn(root map[string]any, keys []string, v any) {
	var cur any = root
	for i, k := range keys {
		last := i == len(keys)-1
		switch t := cur.(type) {
		case map[string]any:
			if last {
				t[k] = v
				return
			}
			cur = t[k]
		case []any:
			n, _ := strconv.Atoi(k)
			if n >= len(t) {
				return
			}
			if last {
				t[n] = v
				return
			}
			cur = t[n]
		default:
			return
		}
	}
}
