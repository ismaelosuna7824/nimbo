// Kubernetes object icons (Deployment, Service, Ingress…) for Kubernetes workspaces: the Build
// canvas, the palette, the inspector and the Architecture view. The artwork is the Kubernetes
// community icon set (see NOTICE.md); each icon is its own asset, fetched only when shown.

type IconDef = readonly [label: string, short: string];

/** id → [label, short name kubectl uses]. Every id has an ./assets/<id>.svg file. */
const ICONS = {
  deploy: ["Deployment", "deploy"],
  sts: ["StatefulSet", "sts"],
  ds: ["DaemonSet", "ds"],
  rs: ["ReplicaSet", "rs"],
  pod: ["Pod", "pod"],
  job: ["Job", "job"],
  cronjob: ["CronJob", "cronjob"],
  svc: ["Service", "svc"],
  ing: ["Ingress", "ing"],
  gateway: ["Gateway", "gtw"],
  httproute: ["Route", "route"],
  cm: ["ConfigMap", "cm"],
  secret: ["Secret", "secret"],
  pvc: ["PersistentVolumeClaim", "pvc"],
  pv: ["PersistentVolume", "pv"],
  sc: ["StorageClass", "sc"],
  vol: ["Volume", "vol"],
  hpa: ["HorizontalPodAutoscaler", "hpa"],
  pdb: ["PodDisruptionBudget", "pdb"],
  netpol: ["NetworkPolicy", "netpol"],
  sa: ["ServiceAccount", "sa"],
  role: ["Role", "role"],
  "c-role": ["ClusterRole", "clusterrole"],
  rb: ["RoleBinding", "rolebinding"],
  crb: ["ClusterRoleBinding", "clusterrolebinding"],
  ns: ["Namespace", "ns"],
  crd: ["Custom resource", "cr"],
  ep: ["Endpoints", "ep"],
  limits: ["LimitRange", "limits"],
  quota: ["ResourceQuota", "quota"],
  node: ["Node", "node"],
  user: ["User", "user"],
  group: ["Group", "group"],
} as const satisfies Record<string, IconDef>;

export type K8sIconId = keyof typeof ICONS;

export type K8sIconInfo = {
  id: K8sIconId;
  /** Kind name ("Deployment"). */
  label: string;
  /** kubectl short name ("deploy"). */
  short: string;
  /** Bundled asset URL of the SVG (same-origin; the Architecture SVG export inlines it). */
  src: string;
};

/** Object kinds (as written in `kind:`) → icon. Kinds not listed are custom resources. */
const KINDS: Record<string, K8sIconId> = {
  Deployment: "deploy",
  StatefulSet: "sts",
  DaemonSet: "ds",
  ReplicaSet: "rs",
  ReplicationController: "rs",
  Pod: "pod",
  Job: "job",
  CronJob: "cronjob",
  Service: "svc",
  Ingress: "ing",
  IngressClass: "ing",
  Gateway: "gateway",
  GatewayClass: "gateway",
  HTTPRoute: "httproute",
  GRPCRoute: "httproute",
  TCPRoute: "httproute",
  TLSRoute: "httproute",
  UDPRoute: "httproute",
  ConfigMap: "cm",
  Secret: "secret",
  PersistentVolumeClaim: "pvc",
  PersistentVolume: "pv",
  StorageClass: "sc",
  HorizontalPodAutoscaler: "hpa",
  PodDisruptionBudget: "pdb",
  NetworkPolicy: "netpol",
  ServiceAccount: "sa",
  Role: "role",
  ClusterRole: "c-role",
  RoleBinding: "rb",
  ClusterRoleBinding: "crb",
  Namespace: "ns",
  CustomResourceDefinition: "crd",
  Endpoints: "ep",
  EndpointSlice: "ep",
  LimitRange: "limits",
  ResourceQuota: "quota",
  Node: "node",
  User: "user",
  Group: "group",
  Volume: "vol",
};

/** All icon ids of the registry. */
export const K8S_ICON_IDS = Object.keys(ICONS) as readonly K8sIconId[];

export function isK8sIconId(id: string): id is K8sIconId {
  return Object.hasOwn(ICONS, id);
}

/** Bundled URL of an icon (Vite turns this pattern into a glob of ./assets). */
export function k8sIconSrc(id: K8sIconId): string {
  return new URL(`./assets/${id}.svg`, import.meta.url).href;
}

/** Icon of a registry id; unknown ids get the custom-resource icon. */
export function k8sIcon(id: string): K8sIconInfo {
  const key: K8sIconId = isK8sIconId(id) ? id : "crd";
  const [label, short] = ICONS[key] as IconDef;
  return { id: key, label, short, src: k8sIconSrc(key) };
}

/** Icon id of an object kind ("Deployment" → deploy); unknown kinds are custom resources. */
export function k8sIconIdForKind(kind: string): K8sIconId {
  return Object.hasOwn(KINDS, kind) ? KINDS[kind] : "crd";
}

/** Icon of an object kind; the label is the kind itself ("IngressClass", custom resource kinds). */
export function k8sIconFor(kind: string): K8sIconInfo {
  const icon = k8sIcon(k8sIconIdForKind(kind));
  return kind ? { ...icon, label: kind } : icon;
}
