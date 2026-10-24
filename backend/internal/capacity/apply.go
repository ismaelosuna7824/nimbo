package capacity

import (
	"math"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Applying a plan writes each service's planned replicas, autoscaler and
// requests/limits into the plan's environment through its per-environment
// settings (PATCH /workflows/{id}/environments/{name}/settings keys):
// Kubernetes knobs "replicas", "hpa.min", "hpa.max", "hpa.cpu",
// "cpu.request:<container>", ...; compose "replicas", "cpus", "memory".

// ApplyValues is what applying svc writes, given the service's settings
// in the environment (kind docker or kubernetes). Only settings whose
// value changes are written; settings the environment can't take are
// listed in Skipped. Nil when nothing would change or be skipped.
func ApplyValues(kind string, svc model.CapacityService, settings []model.EnvSetting) *model.CapacityApply {
	out := &model.CapacityApply{Target: svc.NodeID, Values: map[string]string{}, Changes: []string{}}
	byKey := map[string]model.EnvSetting{}
	for _, s := range settings {
		byKey[s.Key] = s
	}
	set := func(key, label, value string, same func(a, b string) bool) {
		s, ok := byKey[key]
		switch {
		case !ok:
			out.Skipped = append(out.Skipped, label+" → "+value+": not available here")
		case !s.Editable:
			reason := s.Reason
			if reason == "" {
				reason = "not editable in this environment"
			}
			out.Skipped = append(out.Skipped, label+" → "+value+": "+reason)
		case s.Value != "" && same(s.Value, value):
		default:
			out.Values[key] = value
			cur := s.Value
			if cur == "" {
				cur = "unset"
			}
			out.Changes = append(out.Changes, label+": "+cur+" → "+value)
		}
	}
	sameInt := func(a, b string) bool { return strings.TrimSpace(a) == b }
	scales := svc.Role == model.CapacityApp

	if kind == "docker" {
		out.Target = strings.TrimPrefix(svc.NodeID, "service:")
		if scales {
			set("replicas", "Replicas", strconv.Itoa(svc.Replicas), sameInt)
		}
		cores := ParseCPU(svc.CPULimit)
		set("cpus", "CPU limit", trim(cores, 2), func(a, b string) bool { return sameFloat(dockerCPU(a), cores) })
		mem := kubeMem(svc.MemLimit)
		set("memory", "Memory limit", strconv.FormatInt(mem>>20, 10)+"M", func(a, b string) bool { return dockerMem(a) == mem })
		return nonEmpty(out)
	}

	if scales {
		_, hasHPA := byKey["hpa.min"]
		switch {
		case hasHPA && svc.HPA != nil:
			set("hpa.min", "Min replicas", strconv.Itoa(svc.HPA.Min), sameInt)
			set("hpa.max", "Max replicas", strconv.Itoa(svc.HPA.Max), sameInt)
			if _, ok := byKey["hpa.cpu"]; ok {
				set("hpa.cpu", "Target CPU %", strconv.Itoa(svc.HPA.TargetCPU), sameInt)
			}
			// The autoscaler starts from the minimum.
			set("replicas", "Replicas", strconv.Itoa(svc.HPA.Min), sameInt)
		default:
			set("replicas", "Replicas", strconv.Itoa(svc.Replicas), sameInt)
			if svc.HPA != nil {
				out.Skipped = append(out.Skipped, "Autoscaler "+strconv.Itoa(svc.HPA.Min)+"–"+strconv.Itoa(svc.HPA.Max)+" at "+strconv.Itoa(svc.HPA.TargetCPU)+"% CPU: this environment has no HPA for it (add one on the canvas)")
			}
		}
	}
	// Requests/limits go to the pod's first container (the one the
	// settings list first); the others keep theirs.
	container := ""
	for _, s := range settings {
		if c, ok := strings.CutPrefix(s.Key, "cpu.request:"); ok {
			if container == "" {
				container = c
			} else {
				out.Skipped = append(out.Skipped, "Container "+c+": resources unchanged (the plan sizes the pod's first container)")
			}
		}
	}
	if container != "" {
		sameCPU := func(a, b string) bool { return sameFloat(ParseCPU(a), ParseCPU(b)) }
		sameMem := func(a, b string) bool { return kubeMem(a) == kubeMem(b) }
		set("cpu.request:"+container, "CPU request ("+container+")", svc.CPURequest, sameCPU)
		set("cpu.limit:"+container, "CPU limit ("+container+")", svc.CPULimit, sameCPU)
		set("memory.request:"+container, "Memory request ("+container+")", svc.MemRequest, sameMem)
		set("memory.limit:"+container, "Memory limit ("+container+")", svc.MemLimit, sameMem)
	}
	return nonEmpty(out)
}

func nonEmpty(a *model.CapacityApply) *model.CapacityApply {
	if len(a.Values) == 0 && len(a.Skipped) == 0 {
		return nil
	}
	return a
}

func sameFloat(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// ParseCPU parses a Kubernetes CPU quantity ("250m", "0.5", "2") in cores
// (NaN when invalid).
func ParseCPU(q string) float64 {
	q = strings.TrimSpace(q)
	if n, ok := strings.CutSuffix(q, "m"); ok {
		v, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return math.NaN()
		}
		return v / 1000
	}
	v, err := strconv.ParseFloat(q, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

// dockerCPU parses compose cpus ("0.5").
func dockerCPU(q string) float64 {
	v, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(q), `"'`), 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

var kubeMemUnits = map[string]float64{
	"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40,
	"k": 1e3, "K": 1e3, "M": 1e6, "G": 1e9, "T": 1e12, "": 1,
}

// kubeMem parses a Kubernetes memory quantity in bytes (-1 when invalid).
func kubeMem(q string) int64 {
	return parseSize(strings.TrimSpace(q), kubeMemUnits)
}

var dockerMemUnits = map[string]float64{
	"b": 1, "B": 1, "": 1,
	"k": 1 << 10, "K": 1 << 10, "kb": 1 << 10, "KB": 1 << 10, "kB": 1 << 10,
	"m": 1 << 20, "M": 1 << 20, "mb": 1 << 20, "MB": 1 << 20,
	"g": 1 << 30, "G": 1 << 30, "gb": 1 << 30, "GB": 1 << 30,
}

// dockerMem parses a compose memory size ("256M", "1g": binary units) in
// bytes (-1 when invalid).
func dockerMem(q string) int64 {
	return parseSize(strings.Trim(strings.TrimSpace(q), `"'`), dockerMemUnits)
}

func parseSize(q string, units map[string]float64) int64 {
	i := strings.IndexFunc(q, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	num, unit := q, ""
	if i >= 0 {
		num, unit = q[:i], q[i:]
	}
	mul, ok := units[unit]
	v, err := strconv.ParseFloat(num, 64)
	if !ok || err != nil {
		return -1
	}
	return int64(math.Round(v * mul))
}
