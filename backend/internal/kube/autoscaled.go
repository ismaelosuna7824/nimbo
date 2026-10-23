package kube

// Autoscales reports whether a HorizontalPodAutoscaler among the rendered
// objects scales the workload kind/name (in any namespace: environments
// often differ only by namespace).
func (p *Project) Autoscales(kind, name string) bool {
	for _, o := range p.Objects {
		if o.Kind != "HorizontalPodAutoscaler" {
			continue
		}
		ref := dig(o.Obj, "spec", "scaleTargetRef")
		if str(ref, "kind") == kind && str(ref, "name") == name {
			return true
		}
	}
	return false
}
