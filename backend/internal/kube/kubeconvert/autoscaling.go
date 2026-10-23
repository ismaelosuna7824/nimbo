package kubeconvert

import (
	"fmt"
	"regexp"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Horizontal autoscaling of the prod environment, and the rollout and
// spreading settings of the Deployments that scale out.
//
// Stateless services that are not data stores get an autoscaling/v2
// HorizontalPodAutoscaler in prod (Kustomize overlays/prod, Helm
// values-prod.yaml) scaling on CPU utilization relative to their requests.
// Their prod Deployment doesn't set spec.replicas: the HPA owns it, and a
// replica count in the manifest would reset it on every apply
// (https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/#migrating-deployments-and-statefulsets-to-horizontal-autoscaling).
// Plain manifests have a single environment that mirrors compose: no HPA
// (nor PDB) there.

// prodEnv is the environment HPAs are written for.
const prodEnv = "prod"

// prodReplicas is the replica count of a stateless unit in prod: what
// compose asks, at least 2.
func (u *unit) prodReplicas() int { return max(u.replicas, 2) }

// hpaMax is the unit's maxReplicas: the option, at least its minimum.
func (c *converter) hpaMax(u *unit) int { return max(c.opts.Autoscaling.MaxReplicas, u.prodReplicas()) }

// autoscaling decides whether prod autoscales u.
func (c *converter) autoscaling(u *unit) {
	if c.opts.Autoscaling.Disabled || !u.stateless() || u.data {
		return
	}
	if c.opts.Format == model.ConvertManifests {
		if !c.manifestsNoted {
			c.manifestsNoted = true
			c.issue(nil, "info", "autoscaling", "Plain manifests have one environment that mirrors compose, so no HorizontalPodAutoscaler is written: convert to kustomize or helm to get a prod environment with HPAs and PodDisruptionBudgets.")
		}
		return
	}
	u.autoscale = true
	lo, hi, target := u.prodReplicas(), c.hpaMax(u), c.opts.Autoscaling.TargetCPU
	if hi > c.opts.Autoscaling.MaxReplicas {
		c.issue(u, "info", "autoscaling", fmt.Sprintf("Compose runs %d replicas, more than the %d max replicas asked: its HPA scales between %d and %d.", lo, c.opts.Autoscaling.MaxReplicas, lo, hi))
	}
	c.report.Autoscaling = append(c.report.Autoscaling, model.KubeConvertHPA{Service: u.name, Environment: prodEnv, MinReplicas: lo, MaxReplicas: hi, TargetCPU: target})
	c.mapped(u.name, "replicas (prod)", fmt.Sprintf("HorizontalPodAutoscaler: %d–%d replicas, %d%% of the CPU request (%s); the prod Deployment leaves spec.replicas to it", lo, hi, target, u.requests.cpu))
}

// hpa is the HorizontalPodAutoscaler of an autoscaled unit (Helm reads
// its numbers from <key>.autoscaling).
func (b *builder) hpa(u *unit) *yaml.Node {
	c := b.c
	at := u.key + ".autoscaling."
	d := doc("autoscaling/v2", "HorizontalPodAutoscaler", b.meta(u.k8s, b.labels(u.k8s)), "spec", mapping(
		"scaleTargetRef", mapping("apiVersion", "apps/v1", "kind", "Deployment", "name", u.k8s),
		"minReplicas", b.v.scalar(at+"minReplicas", num(int64(u.prodReplicas()))),
		"maxReplicas", b.v.scalar(at+"maxReplicas", num(int64(c.hpaMax(u)))),
		"metrics", seq(mapping(
			"type", "Resource",
			"resource", mapping("name", "cpu", "target", mapping(
				"type", "Utilization",
				"averageUtilization", b.v.scalar(at+"targetCPUUtilizationPercentage", num(int64(c.opts.Autoscaling.TargetCPU))),
			)),
		)),
		// Scale up fast (Kubernetes' default: double or +4 pods every 15 s,
		// no stabilization), scale down conservatively: 5 minutes of
		// stabilization, then at most half the pods per minute, so a dip
		// between bursts doesn't drop capacity.
		"behavior", mapping(
			"scaleUp", mapping(
				"stabilizationWindowSeconds", 0,
				"policies", seq(
					mapping("type", "Percent", "value", 100, "periodSeconds", 15),
					mapping("type", "Pods", "value", 4, "periodSeconds", 15),
				),
				"selectPolicy", "Max",
			),
			"scaleDown", mapping(
				"stabilizationWindowSeconds", 300,
				"policies", seq(mapping("type", "Percent", "value", 50, "periodSeconds", 60)),
			),
		),
	))
	return comment(d, "Scales "+u.k8s+" on its CPU utilization relative to its CPU request: requests must stay set",
		"on every container for the HPA to compute it.")
}

// rollingUpdate replaces pods one at a time without dropping below the
// desired count: maxUnavailable 0 keeps full capacity during a rollout,
// maxSurge 1 adds one new pod at a time.
func rollingUpdate() *yaml.Node {
	return mapping("type", "RollingUpdate", "rollingUpdate", mapping("maxUnavailable", 0, "maxSurge", 1))
}

// spread prefers putting the replicas of u on different nodes
// (ScheduleAnyway: a single-node cluster still schedules them).
func (b *builder) spread(u *unit) *yaml.Node {
	return seq(mapping(
		"maxSkew", 1,
		"topologyKey", "kubernetes.io/hostname",
		"whenUnsatisfiable", "ScheduleAnyway",
		"labelSelector", mapping("matchLabels", b.selector(u)),
	))
}

var labelValueRe = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`)

// version is the app.kubernetes.io/version label of u's workload: its
// image tag (nil when the image has none, or it isn't a valid label).
func (b *builder) version(u *unit) *yaml.Node {
	ref := compose.ParseImage(u.image)
	if ref == nil || ref.Digest != "" || !labelValueRe.MatchString(ref.Tag) {
		return nil
	}
	return b.v.expr(ref.Tag, "{{ .Values."+u.key+".image.tag | toString | trunc 63 | trimSuffix \"-\" | quote }}")
}

// replicas is the spec.replicas of u's workload. Helm leaves it out when
// the HPA manages the Deployment (the standard
// `if not .Values.<svc>.autoscaling.enabled` pattern).
func (b *builder) replicas(u *unit) *yaml.Node {
	v := num(int64(u.replicas))
	if h, ok := b.v.(*helmValuer); ok && u.autoscale {
		return h.scalarIf(u.key+".replicaCount", v, "not .Values."+u.key+".autoscaling.enabled")
	}
	return b.v.scalar(u.key+".replicaCount", v)
}
