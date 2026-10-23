package kuberules

// API versions removed from (or deprecated in) Kubernetes, from the
// deprecation guide (https://kubernetes.io/docs/reference/using-api/deprecation-guide/).
// Keys are "<apiVersion>/<Kind>"; "*" matches every kind of the version.

type apiChange struct {
	Replacement string // apiVersion to use ("" when the kind is gone)
	Release     string // release that removed (or will remove) it
	Note        string // what else changes when migrating
}

var removedAPIs = map[string]apiChange{
	"extensions/v1beta1/Deployment":                                       {"apps/v1", "1.16", "spec.selector is required"},
	"extensions/v1beta1/DaemonSet":                                        {"apps/v1", "1.16", "spec.selector is required"},
	"extensions/v1beta1/ReplicaSet":                                       {"apps/v1", "1.16", "spec.selector is required"},
	"extensions/v1beta1/NetworkPolicy":                                    {"networking.k8s.io/v1", "1.16", ""},
	"extensions/v1beta1/PodSecurityPolicy":                                {"", "1.25", "PodSecurityPolicy was replaced by Pod Security Admission (namespace labels)"},
	"extensions/v1beta1/Ingress":                                          {"networking.k8s.io/v1", "1.22", "backends become service.name and service.port.number/name, and pathType is required"},
	"apps/v1beta1/*":                                                      {"apps/v1", "1.16", "spec.selector is required"},
	"apps/v1beta2/*":                                                      {"apps/v1", "1.16", "spec.selector is required"},
	"networking.k8s.io/v1beta1/Ingress":                                   {"networking.k8s.io/v1", "1.22", "backends become service.name and service.port.number/name, and pathType is required"},
	"networking.k8s.io/v1beta1/IngressClass":                              {"networking.k8s.io/v1", "1.22", ""},
	"admissionregistration.k8s.io/v1beta1/MutatingWebhookConfiguration":   {"admissionregistration.k8s.io/v1", "1.22", "webhooks[].sideEffects and admissionReviewVersions are required"},
	"admissionregistration.k8s.io/v1beta1/ValidatingWebhookConfiguration": {"admissionregistration.k8s.io/v1", "1.22", "webhooks[].sideEffects and admissionReviewVersions are required"},
	"apiextensions.k8s.io/v1beta1/CustomResourceDefinition":               {"apiextensions.k8s.io/v1", "1.22", "spec.versions[].schema.openAPIV3Schema is required"},
	"apiregistration.k8s.io/v1beta1/APIService":                           {"apiregistration.k8s.io/v1", "1.22", ""},
	"authentication.k8s.io/v1beta1/TokenReview":                           {"authentication.k8s.io/v1", "1.22", ""},
	"authorization.k8s.io/v1beta1/*":                                      {"authorization.k8s.io/v1", "1.22", ""},
	"certificates.k8s.io/v1beta1/CertificateSigningRequest":               {"certificates.k8s.io/v1", "1.22", "spec.signerName is required"},
	"coordination.k8s.io/v1beta1/Lease":                                   {"coordination.k8s.io/v1", "1.22", ""},
	"rbac.authorization.k8s.io/v1beta1/*":                                 {"rbac.authorization.k8s.io/v1", "1.22", ""},
	"rbac.authorization.k8s.io/v1alpha1/*":                                {"rbac.authorization.k8s.io/v1", "1.22", ""},
	"scheduling.k8s.io/v1beta1/PriorityClass":                             {"scheduling.k8s.io/v1", "1.22", ""},
	"scheduling.k8s.io/v1alpha1/PriorityClass":                            {"scheduling.k8s.io/v1", "1.17", ""},
	"storage.k8s.io/v1beta1/CSIDriver":                                    {"storage.k8s.io/v1", "1.22", ""},
	"storage.k8s.io/v1beta1/CSINode":                                      {"storage.k8s.io/v1", "1.22", ""},
	"storage.k8s.io/v1beta1/StorageClass":                                 {"storage.k8s.io/v1", "1.22", ""},
	"storage.k8s.io/v1beta1/VolumeAttachment":                             {"storage.k8s.io/v1", "1.22", ""},
	"storage.k8s.io/v1beta1/CSIStorageCapacity":                           {"storage.k8s.io/v1", "1.27", ""},
	"batch/v1beta1/CronJob":                                               {"batch/v1", "1.25", ""},
	"batch/v2alpha1/CronJob":                                              {"batch/v1", "1.21", ""},
	"discovery.k8s.io/v1beta1/EndpointSlice":                              {"discovery.k8s.io/v1", "1.25", "topology becomes deprecatedTopology / zone / hints"},
	"events.k8s.io/v1beta1/Event":                                         {"events.k8s.io/v1", "1.25", ""},
	"autoscaling/v2beta1/HorizontalPodAutoscaler":                         {"autoscaling/v2", "1.25", "metrics targets become target: {type, averageUtilization}"},
	"autoscaling/v2beta2/HorizontalPodAutoscaler":                         {"autoscaling/v2", "1.26", ""},
	"policy/v1beta1/PodDisruptionBudget":                                  {"policy/v1", "1.25", "an empty selector now selects every pod of the namespace"},
	"policy/v1beta1/PodSecurityPolicy":                                    {"", "1.25", "PodSecurityPolicy was replaced by Pod Security Admission (namespace labels)"},
	"node.k8s.io/v1beta1/RuntimeClass":                                    {"node.k8s.io/v1", "1.25", ""},
	"flowcontrol.apiserver.k8s.io/v1beta1/*":                              {"flowcontrol.apiserver.k8s.io/v1", "1.26", ""},
	"flowcontrol.apiserver.k8s.io/v1beta2/*":                              {"flowcontrol.apiserver.k8s.io/v1", "1.29", ""},
	"flowcontrol.apiserver.k8s.io/v1beta3/*":                              {"flowcontrol.apiserver.k8s.io/v1", "1.32", ""},
}

// deprecatedAPIs are still served by the pinned release.
var deprecatedAPIs = map[string]apiChange{
	"v1/Endpoints":              {"discovery.k8s.io/v1", "", "use an EndpointSlice (kind EndpointSlice) for manually managed endpoints"},
	"v1/ComponentStatus":        {"", "", "query the components' health endpoints instead"},
	"resource.k8s.io/v1beta1/*": {"resource.k8s.io/v1", "", ""},
	"resource.k8s.io/v1beta2/*": {"resource.k8s.io/v1", "", ""},
}

func lookupAPI(table map[string]apiChange, apiVersion, kind string) (apiChange, bool) {
	if c, ok := table[apiVersion+"/"+kind]; ok {
		return c, true
	}
	c, ok := table[apiVersion+"/*"]
	return c, ok
}
