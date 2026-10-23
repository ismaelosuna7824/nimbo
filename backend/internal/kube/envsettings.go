package kube

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Per-environment knobs: the settings that usually change between
// environments (replicas, autoscaler bounds, requests/limits, image tags)
// edited without YAML. A knob is written into the environment's own files
// only, so the base and the other environments keep their values:
// Kustomize knobs go where the overlay already sets the field (its
// replicas/images transformers, patches or resources), else into a new
// replicas/images entry or a strategic-merge patch of the overlay; Helm
// knobs set the values key the chart renders the field from in the
// environment's values file.

// envKnob is one knob of a workload in an environment.
type envKnob struct {
	model.EnvSetting
	obj  *Object // the object holding the field (the workload or its HPA)
	path string  // field path in obj
}

// Knob keys (EnvSetting.Key); container knobs end in ":<container>".
const (
	knobReplicas = "replicas"
	knobHPAMin   = "hpa.min"
	knobHPAMax   = "hpa.max"
	knobHPACPU   = "hpa.cpu"
	knobImage    = "image"
	knobCPUReq   = "cpu.request"
	knobCPULim   = "cpu.limit"
	knobMemReq   = "memory.request"
	knobMemLim   = "memory.limit"
)

// EnvTargets are the workloads of p, by envKey.
func EnvTargets(p *Project, wf *model.Workflow) []model.EnvTarget {
	out := []model.EnvTarget{}
	multi := multiSource(wf)
	for _, o := range p.Objects {
		if isWorkload(o) {
			out = append(out, model.EnvTarget{ID: envKey(o, multi), Label: baseName(o), Kind: o.Kind})
		}
	}
	return out
}

// envTarget resolves a target (envKey or node ID; "" = the first workload).
func envTarget(p *Project, wf *model.Workflow, target string) (*Object, error) {
	multi := multiSource(wf)
	for _, o := range p.Objects {
		if isWorkload(o) && (target == "" || envKey(o, multi) == target || o.ID == target) {
			return o, nil
		}
	}
	if target == "" {
		return nil, &EditError{Msg: "the environment renders no workload", NotFound: true}
	}
	return nil, &EditError{Msg: fmt.Sprintf("workload %q not found in environment %q", target, p.Environment), NotFound: true}
}

// EnvSettings lists the knobs of workload target in p's environment (base:
// the project rendered without one, for the inherited values).
func EnvSettings(p, base *Project, wf *model.Workflow, target string) (model.EnvSettings, error) {
	out := model.EnvSettings{Environment: p.Environment, Targets: EnvTargets(p, wf), Settings: []model.EnvSetting{}}
	o, err := envTarget(p, wf, target)
	if err != nil {
		if len(out.Targets) == 0 {
			out.Note = "This environment renders no workload."
			return out, nil
		}
		return out, err
	}
	out.Target = envKey(o, multiSource(wf))
	r := p.Source(o)
	switch {
	case r == nil:
	case !TakesEnvironments(r.Source.Kind):
		out.Note = "Plain manifests deploy the same in every environment: edit them on the canvas or in Code."
	case r.Env == "":
		out.Note = fmt.Sprintf("%s %s has no %q environment.", r.Source.Kind, r.Source.Path, p.Environment)
	case r.Source.Kind == model.KubeKustomize:
		out.File = p.kustomizationFile(r.Env)
	default:
		out.File = r.Env
	}
	if p.scalerOf(o) != nil && o.Kind != "StatefulSet" {
		out.Note = strings.TrimSpace(out.Note + " An autoscaler manages the replicas while it runs: its min/max matter more than Replicas.")
	}
	for _, k := range p.knobs(o, base, wf) {
		out.Settings = append(out.Settings, k.EnvSetting)
	}
	return out, nil
}

// knobs lists o's knobs with their values here and in base, located.
func (p *Project) knobs(o *Object, base *Project, wf *model.Workflow) []*envKnob {
	var bo *Object
	if base != nil {
		bo = findByKey(base, o, multiSource(wf))
	}
	baseVal := func(obj *Object, path string) string {
		if obj == nil {
			return ""
		}
		return fieldText(obj, path)
	}
	var out []*envKnob
	add := func(k *envKnob, basePath string, baseObj *Object) {
		k.Value = fieldText(k.obj, k.path)
		k.Base = baseVal(baseObj, basePath)
		out = append(out, k)
	}
	if o.Kind == "Deployment" || o.Kind == "StatefulSet" || o.Kind == "ReplicaSet" {
		add(&envKnob{EnvSetting: model.EnvSetting{Key: knobReplicas, Label: "Replicas", Group: "Scaling", Type: model.EnvSettingInt}, obj: o, path: "spec.replicas"}, "spec.replicas", bo)
	}
	if h := p.scalerOf(o); h != nil {
		var bh *Object
		if bo != nil {
			bh = base.scalerOf(bo)
		}
		add(&envKnob{EnvSetting: model.EnvSetting{Key: knobHPAMin, Label: "Min replicas", Group: "Autoscaling", Type: model.EnvSettingInt}, obj: h, path: "spec.minReplicas"}, "spec.minReplicas", bh)
		add(&envKnob{EnvSetting: model.EnvSetting{Key: knobHPAMax, Label: "Max replicas", Group: "Autoscaling", Type: model.EnvSettingInt}, obj: h, path: "spec.maxReplicas"}, "spec.maxReplicas", bh)
		if i := cpuMetric(h); i >= 0 {
			pth := fmt.Sprintf("spec.metrics[%d].resource.target.averageUtilization", i)
			bpth := pth
			if bh != nil {
				bpth = fmt.Sprintf("spec.metrics[%d].resource.target.averageUtilization", cpuMetric(bh))
			}
			add(&envKnob{EnvSetting: model.EnvSetting{Key: knobHPACPU, Label: "Target CPU %", Group: "Autoscaling", Type: model.EnvSettingInt}, obj: h, path: pth}, bpth, bh)
		}
	}
	spec, keys := podSpec(o)
	prefix := strings.Join(keys, ".")
	for _, c := range list(spec["containers"]) {
		name := str(c, "name")
		cp := prefix + ".containers[name=" + name + "]"
		group := "Container " + name
		img := &envKnob{EnvSetting: model.EnvSetting{Key: knobImage + ":" + name, Label: "Image tag", Group: group, Type: model.EnvSettingString}, obj: o, path: cp + ".image"}
		img.Value = imageTag(str(c, "image"))
		if bo != nil {
			bspec, _ := podSpec(bo)
			for _, bc := range list(bspec["containers"]) {
				if str(bc, "name") == name {
					img.Base = imageTag(str(bc, "image"))
				}
			}
		}
		out = append(out, img)
		for _, q := range []struct{ key, label, typ, field string }{
			{knobCPUReq, "CPU request", model.EnvSettingCPU, "resources.requests.cpu"},
			{knobCPULim, "CPU limit", model.EnvSettingCPU, "resources.limits.cpu"},
			{knobMemReq, "Memory request", model.EnvSettingMemory, "resources.requests.memory"},
			{knobMemLim, "Memory limit", model.EnvSettingMemory, "resources.limits.memory"},
		} {
			add(&envKnob{EnvSetting: model.EnvSetting{Key: q.key + ":" + name, Label: q.label, Group: group, Type: q.typ}, obj: o, path: cp + "." + q.field}, cp+"."+q.field, bo)
		}
	}
	for _, k := range out {
		p.locateKnob(k, base)
	}
	return out
}

func findByKey(p *Project, o *Object, multi bool) *Object {
	key := envKey(o, multi)
	for _, x := range p.Objects {
		if x.Kind == o.Kind && envKey(x, multi) == key {
			return x
		}
	}
	return nil
}

// fieldText is the scalar at a field path of obj ("" when absent).
func fieldText(obj *Object, fieldPath string) string {
	segs, err := ParsePath(fieldPath)
	if err != nil {
		return ""
	}
	keys, missing, ok := concrete(obj.Obj, segs)
	if !ok || missing {
		return ""
	}
	v, ok := at(obj.Obj, keys)
	if !ok || v == nil || !isScalar(v) {
		return ""
	}
	return scalarString(v)
}

// imageTag is the tag of an image reference ("latest" when untagged).
func imageTag(image string) string {
	ref := compose.ParseImage(image)
	if ref == nil {
		return ""
	}
	if ref.Tag == "" && ref.Digest == "" {
		return "latest"
	}
	return ref.Tag
}

// field is where the knob's field comes from in p.
func (p *Project) field(k *envKnob) *fieldSource {
	segs, _ := ParsePath(k.path)
	return p.fieldSources(k.obj, [][]seg{segs})[0]
}

// inDir reports whether file is dir or below it.
func inDir(file, dir string) bool {
	return dir != "" && (file == dir || dir == "." || strings.HasPrefix(file, dir+"/"))
}

// locateKnob fills Editable, Reason and Overridden.
func (p *Project) locateKnob(k *envKnob, base *Project) {
	r := p.Source(k.obj)
	switch {
	case p.Environment == "":
		k.Reason = "the base: edit it on the canvas or in Code (environments change it on top)"
		return
	case r == nil:
		k.Reason = "the object's source is unknown"
		return
	case !TakesEnvironments(r.Source.Kind):
		k.Reason = "plain manifests deploy the same in every environment"
		return
	case r.Env == "":
		k.Reason = fmt.Sprintf("%s %s has no %q environment", r.Source.Kind, r.Source.Path, p.Environment)
		return
	}
	if r.Source.Kind == model.KubeKustomize {
		fs := p.field(k)
		// Overridden means resettable: a transformer entry or a patch of
		// the overlay (a resource only the overlay has has no base value).
		k.Overridden = fs.Editable && inDir(fs.File, r.Env) && (fs.Via == ViaKustomization || fs.Via == ViaPatch)
		k.Editable = true
		if k.obj.generated {
			k.Editable, k.Reason = false, "generated by a kustomization generator"
		}
		return
	}
	fs := p.helmValuesSource(k, base)
	if fs == nil || fs.Via != ViaValues || !fs.Editable {
		reason := "the chart does not expose it as a value"
		if fs != nil && fs.Reason != "" {
			reason = fs.Reason
		}
		k.Reason = reason
		return
	}
	k.Editable = true
	ed := p.Edit()
	for _, key := range fs.ValuesKeys {
		if ed.definesValuesKey(r.Env, key) {
			k.Overridden = true
		}
	}
	// Not rendered here (replicas next to an autoscaler): show what the
	// environment's values file sets, so the override stays visible.
	if k.Value == "" && k.Overridden && len(fs.ValuesKeys) == 1 {
		if f, err := ed.file(r.Env); err == nil && len(f.Docs()) > 0 {
			if keys, err := valuesPath(fs.ValuesKeys[0]); err == nil {
				if n := f.Docs()[0].Doc.Node(keys...); n != nil && n.Kind == yaml.ScalarNode {
					k.Value = n.Value
				}
			}
		}
	}
}

// helmValuesSource is where a Helm field comes from: this render, or (when
// the field is not rendered here, e.g. replicas next to an autoscaler) the
// base render of the same object.
func (p *Project) helmValuesSource(k *envKnob, base *Project) *fieldSource {
	fs := p.field(k)
	if fs.Via == ViaValues || base == nil || fieldText(k.obj, k.path) != "" {
		return fs
	}
	bo := findByKey(base, k.obj, false)
	if bo == nil {
		bo = base.objectNamed(k.obj.Kind, k.obj.Namespace, k.obj.Name)
	}
	if bo == nil || fieldText(bo, k.path) == "" {
		return fs
	}
	segs, _ := ParsePath(k.path)
	return base.fieldSources(bo, [][]seg{segs})[0]
}

var quantityRe = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?|\.[0-9]+)(m|k|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)?$`)

// knobValue checks and types a knob's new value.
func knobValue(k *envKnob, text string) (any, error) {
	text = strings.TrimSpace(text)
	switch k.Type {
	case model.EnvSettingInt:
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			return nil, editErrorf("%s must be a whole number", k.Label)
		}
		if (k.Key == knobHPAMin || k.Key == knobHPAMax) && n < 1 {
			return nil, editErrorf("%s must be at least 1", k.Label)
		}
		if k.Key == knobHPACPU && (n < 1 || n > 1000) {
			return nil, editErrorf("%s must be between 1 and 1000", k.Label)
		}
		return n, nil
	case model.EnvSettingCPU, model.EnvSettingMemory:
		if !quantityRe.MatchString(text) {
			return nil, editErrorf("%s %q is not a quantity (e.g. 250m, 1, 512Mi, 1Gi)", k.Label, text)
		}
		return text, nil
	}
	if text == "" || strings.ContainsAny(text, " :@/") {
		return nil, editErrorf("%s %q is not an image tag", k.Label, text)
	}
	return text, nil
}

// SetEnvSettings writes knob values (nil: drop the environment's override,
// back to the base) for workload target into e's environment files. e
// edits a project rendered for the environment; base is rendered without.
func (e *Editor) SetEnvSettings(base *Project, wf *model.Workflow, target string, values map[string]*string) error {
	p := e.p
	if p.Environment == "" {
		return editErrorf("pick an environment: the base is edited on the canvas or in Code")
	}
	o, err := envTarget(p, wf, target)
	if err != nil {
		return err
	}
	knobs := map[string]*envKnob{}
	for _, k := range p.knobs(o, base, wf) {
		knobs[k.Key] = k
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		k := knobs[key]
		if k == nil {
			return &EditError{Msg: fmt.Sprintf("%s has no setting %q", baseName(o), key), NotFound: true}
		}
		if !k.Editable {
			return editErrorf("%s of %s: %s", k.Label, baseName(o), k.Reason)
		}
		var v any
		if values[key] != nil {
			if v, err = knobValue(k, *values[key]); err != nil {
				return err
			}
		}
		if p.Source(k.obj).Source.Kind == model.KubeKustomize {
			err = e.setKustKnob(k, v)
		} else {
			err = e.setHelmKnob(k, base, v)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// fullImage is the knob's container image with tag.
func fullImage(k *envKnob, tag string) string {
	ref := compose.ParseImage(fieldText(k.obj, k.path))
	repo := ""
	if ref != nil {
		repo = ref.Repo
	}
	return repo + ":" + tag
}

func (e *Editor) setKustKnob(k *envKnob, v any) error {
	p := e.p
	overlay := p.Source(k.obj).Env
	fs := p.field(k)
	inOverlay := fs.Editable && inDir(fs.File, overlay)
	isImage := strings.HasPrefix(k.Key, knobImage+":")
	if v == nil {
		if !inOverlay {
			return nil // already the base's
		}
		switch fs.Via {
		case ViaKustomization:
			f, s, err := e.segment(fs.File, 0)
			if err != nil {
				return err
			}
			item := []string{fs.transformer, strconv.Itoa(fs.item)}
			var entry map[string]any
			if n := s.Doc.Node(item...); n != nil {
				_ = n.Decode(&entry)
			}
			// An images entry that also renames or pins a digest keeps
			// that: only its tag goes.
			if fs.transformer == "images" && (str(entry, "newName") != "" || str(entry, "digest") != "") {
				err = s.Doc.Delete(append(item, "newTag"), false)
			} else {
				err = s.Doc.DeleteItem([]string{fs.transformer}, fs.item, true)
			}
			if err != nil {
				return editErrorf("%v", err)
			}
			f.Sync(s)
			e.touch(fs.File)
			return nil
		case ViaPatch:
			return e.Unset(k.obj, k.path)
		}
		return editErrorf("%s comes from %s, which only %s has: change it instead", k.Label, fs.File, p.Environment)
	}
	if isImage {
		v = fullImage(k, v.(string))
	}
	if inOverlay {
		return e.write(k.obj, fs, v)
	}
	kf := p.kustomizationFile(overlay)
	if kf == "" {
		return &EditError{Msg: overlay + " has no kustomization.yaml", NotFound: true}
	}
	switch {
	case k.Key == knobReplicas:
		return e.appendKust(kf, "replicas", compose.Map("name", compose.Str(baseName(k.obj)), "count", compose.Int(v.(int))))
	case isImage:
		ref := compose.ParseImage(v.(string))
		return e.appendKust(kf, "images", compose.Map("name", compose.Str(ref.Repo), "newTag", compose.Str(ref.Tag)))
	case k.Key == knobHPACPU:
		// A strategic-merge patch replaces the metrics list: write it whole.
		metrics := deepCopy(dig(k.obj.Obj, "spec", "metrics")).([]any)
		i := cpuMetric(k.obj)
		setIn(metrics[i].(map[string]any), []string{"resource", "target", "averageUtilization"}, v)
		return e.overlayPatch(kf, k.obj, "spec.metrics", metrics)
	}
	return e.overlayPatch(kf, k.obj, k.path, v)
}

// appendKust appends an item to a list of the kustomization kf.
func (e *Editor) appendKust(kf, list string, item *yaml.Node) error {
	f, s, err := e.segment(kf, 0)
	if err != nil {
		return err
	}
	if err := s.Doc.AppendItem([]string{list}, item); err != nil {
		return editErrorf("%s: %v", kf, err)
	}
	f.Sync(s)
	e.touch(kf)
	return nil
}

// overlayPatch writes fieldPath of obj into a strategic-merge patch of the
// overlay whose kustomization is kf: the overlay's existing patch file for
// the object, else a new <kind>-<name>-patch.yaml listed in its patches.
func (e *Editor) overlayPatch(kf string, obj *Object, fieldPath string, value any) error {
	segs, err := ParsePath(fieldPath)
	if err != nil {
		return editErrorf("%v", err)
	}
	dir := path.Dir(kf)
	kind, name := obj.Kind, baseName(obj)
	if obj.orgKind != "" {
		kind = obj.orgKind
	}
	kfile, err := e.file(kf)
	if err != nil {
		return err
	}
	var k map[string]any
	if docs := kfile.Docs(); len(docs) == 0 || docs[0].Doc.Root.Decode(&k) != nil {
		return editErrorf("%s can't be read", kf)
	}
	var entries []string
	for _, it := range list(k["patches"]) {
		if p := str(it, "path"); p != "" && dig(it, "target") == nil {
			entries = append(entries, p)
		}
	}
	entries = append(entries, anyStrings(k["patchesStrategicMerge"])...)
	for _, rel := range entries {
		file := cleanRel(path.Join(dir, rel))
		f, err := e.file(file)
		if err != nil || len(f.Docs()) != 1 {
			continue
		}
		var m map[string]any
		if f.Docs()[0].Doc.Root.Decode(&m) == nil && str(m, "kind") == kind && str(m, "metadata", "name") == name {
			return e.createIn(file, 0, segs, value)
		}
	}
	file := joinRel(dir, strings.ToLower(kind)+"-"+safeName(name)+"-patch.yaml")
	for n := 2; e.p.fs.exists(file) || e.changed[file]; n++ {
		file = joinRel(dir, fmt.Sprintf("%s-%s-patch-%d.yaml", strings.ToLower(kind), safeName(name), n))
	}
	skeleton := fmt.Sprintf("# What %s changes in %s %s (a strategic-merge patch on the base).\napiVersion: %s\nkind: %s\nmetadata:\n  name: %s\n",
		e.p.Environment, kind, name, obj.APIVersion, kind, name)
	e.yaml[file] = ParseFile(file, []byte(skeleton))
	e.touch(file)
	if err := e.createIn(file, 0, segs, value); err != nil {
		return err
	}
	return e.appendKust(kf, "patches", compose.Map("path", compose.Str(relDir(dir, file))))
}

func (e *Editor) setHelmKnob(k *envKnob, base *Project, v any) error {
	p := e.p
	envFile := p.Source(k.obj).Env
	fs := p.helmValuesSource(k, base)
	if fs == nil || fs.Via != ViaValues {
		return editErrorf("%s: the chart does not expose it as a value", k.Label)
	}
	if v == nil {
		for _, key := range fs.ValuesKeys {
			if e.definesValuesKey(envFile, key) {
				if err := e.unsetValuesKey(envFile, key); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if strings.HasPrefix(k.Key, knobImage+":") {
		v = fullImage(k, v.(string))
	}
	text := scalarString(v)
	parts := []string{text}
	simple := len(fs.pattern.keys) == 1 && fs.pattern.parts[0] == "" && fs.pattern.parts[1] == ""
	if !simple {
		var ok bool
		if parts, ok = fs.pattern.split(text, fs.cur); !ok {
			return editErrorf("%q does not fit the chart's format %s", text, fs.pattern.describe())
		}
	}
	for i, key := range fs.pattern.keys {
		if i < len(fs.cur) && parts[i] == fs.cur[i] && !simple {
			continue
		}
		val := any(parts[i])
		if simple {
			val = v
		}
		// Back to what the chart and the shared values files give: drop
		// the environment's override instead of repeating the value.
		if lower, ok := p.lowerValue(p.Source(k.obj).helm, envFile, key); ok && lower == parts[i] {
			if e.definesValuesKey(envFile, key) {
				if err := e.unsetValuesKey(envFile, key); err != nil {
					return err
				}
			}
			continue
		}
		if err := e.setValuesKey(envFile, key, val); err != nil {
			return err
		}
	}
	return nil
}
