package compose

// ImageBase relates a service's built image to the base image of its final
// stage, so the image's size can be split into base and own layers.
type ImageBase struct {
	// Image is the external FROM image the final stage builds on
	// (following FROM <stage> chains), ARGs applied.
	Image string
	// OwnLayers is the number of image history entries the Dockerfile adds
	// on top of Image: BuildKit records one entry per instruction of every
	// stage of the chain, FROM excluded.
	OwnLayers int
}

// ImageBases returns the ImageBase of every service built from a parsed
// Dockerfile (inline Dockerfiles and unreadable ones are left out).
func (p *Project) ImageBases() map[string]ImageBase {
	out := map[string]ImageBase{}
	if p.Model == nil {
		return out
	}
	for name, s := range p.Model.Services {
		if s.Build == nil || s.Build.DockerfileInline != "" {
			continue
		}
		stages, target := p.stages(s)
		if target < 0 {
			continue
		}
		byRef := map[string]int{}
		for i, st := range stages {
			byRef[stageRef(st.Stage)] = i
		}
		own, cur := 0, target
		seen := map[int]bool{}
		for !seen[cur] {
			seen[cur] = true
			own += len(stages[cur].Instructions) - 1
			next, ok := byRef[stages[cur].FromStage]
			if stages[cur].FromStage == "" || !ok {
				break
			}
			cur = next
		}
		out[name] = ImageBase{Image: stages[cur].From, OwnLayers: own}
	}
	return out
}
