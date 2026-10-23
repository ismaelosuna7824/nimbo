import { describe, expect, test } from "bun:test"
import type { CodeProblem } from "@/lib/api/types"
import { doubleQuantity, kubeQuickFixes, replacementApiVersion } from "./fixes"
import { applyChanges, parseFieldPath } from "./yaml-edit"

const DEPLOY = `# the web app
apiVersion: v1
kind: ConfigMap
metadata:
  name: other
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      containers:
        - name: sidecar
          image: busybox:1.36
        - name: web # main
          image: ghcr.io/acme/web:latest
          ports:
            - name: http
              containerPort: 8080
          resources:
            requests:
              memory: 200Mi
          securityContext: {}
      volumes: []
`

const problem = (code: string, attribute: string | undefined, line: number, extra: Partial<CodeProblem> = {}): CodeProblem => ({
  severity: "warning",
  code,
  message: code,
  line,
  column: 1,
  endLine: line,
  endColumn: 1000,
  attribute,
  ...extra,
})

/** The text after the first fix of a problem. */
function fixed(pr: CodeProblem, text = DEPLOY, env = {}): string {
  const fixes = kubeQuickFixes(pr, text, env)
  expect(fixes.length).toBe(1)
  return applyChanges(text, fixes[0].changes ?? [])
}

const C1 = "spec.template.spec.containers[1]"

describe("k8s quick fixes", () => {
  test("field paths", () => {
    expect(parseFieldPath("spec.template.spec.containers[0].image")).toEqual(["spec", "template", "spec", "containers", 0, "image"])
    expect(parseFieldPath("spec.containers[name=web].ports[1]")).toEqual(["spec", "containers", { name: "web" }, "ports", 1])
  })

  test("k8s_local_image: <repo>:dev and imagePullPolicy IfNotPresent", () => {
    const out = fixed(problem("k8s_local_image", `${C1}.image`, 18))
    expect(out).toContain("          image: ghcr.io/acme/web:dev\n")
    expect(out).toContain("          securityContext: {}\n          imagePullPolicy: IfNotPresent\n      volumes: []")
    expect(kubeQuickFixes(problem("k8s_local_image", `${C1}.image`, 18), DEPLOY)[0].name).toBe("Use ghcr.io/acme/web:dev with imagePullPolicy: IfNotPresent")
  })

  test("k8s_local_image replaces an Always pull policy", () => {
    const text = DEPLOY.replace("          image: ghcr.io/acme/web:latest\n", "          image: web\n          imagePullPolicy: Always\n")
    const out = fixed(problem("k8s_local_image", `${C1}.image`, 18), text)
    expect(out).toContain("          image: web:dev\n          imagePullPolicy: IfNotPresent\n")
  })

  test("k8s_latest_tag: the catalog's tag, a local build, or nothing", () => {
    const text = DEPLOY.replace("ghcr.io/acme/web:latest", "nginx")
    const out = fixed(problem("k8s_latest_tag", `${C1}.image`, 18), text, { images: [{ image: "nginx", tag: "1.27-alpine" }] })
    expect(out).toContain("          image: nginx:1.27-alpine\n")
    expect(kubeQuickFixes(problem("k8s_latest_tag", `${C1}.image`, 18), text)).toEqual([])
    const local = fixed(problem("k8s_latest_tag", `${C1}.image`, 18), DEPLOY, { localImages: ["ghcr.io/acme/web:dev"] })
    expect(local).toContain("image: ghcr.io/acme/web:dev\n")
  })

  test("k8s_no_requests merges cpu/memory into existing requests", () => {
    const out = fixed(problem("k8s_no_requests", `${C1}.resources`, 22))
    expect(out).toContain("          resources:\n            requests:\n              memory: 200Mi\n              cpu: 100m\n          securityContext")
  })

  test("k8s_no_requests creates resources when missing (first container, no field path: by line)", () => {
    const out = fixed(problem("k8s_no_requests", undefined, 16))
    expect(out).toContain("        - name: sidecar\n          image: busybox:1.36\n          resources:\n            requests:\n              cpu: 100m\n              memory: 128Mi\n        - name: web")
  })

  test("k8s_no_memory_limit: twice the request", () => {
    const out = fixed(problem("k8s_no_memory_limit", `${C1}.resources`, 22))
    expect(out).toContain("              memory: 200Mi\n            limits:\n              memory: 400Mi\n")
    const none = fixed(problem("k8s_no_memory_limit", "spec.template.spec.containers[0].resources", 15))
    expect(none).toContain("          image: busybox:1.36\n          resources:\n            limits:\n              memory: 256Mi\n")
    expect(doubleQuantity("1Gi")).toBe("2Gi")
    expect(doubleQuantity("0.5Gi")).toBe("1Gi")
    expect(doubleQuantity("lots")).toBeNull()
  })

  test("probes: httpGet / on an http port, tcpSocket otherwise", () => {
    const out = fixed(problem("k8s_no_readiness_probe", C1, 17))
    expect(out).toContain("          securityContext: {}\n          readinessProbe:\n            httpGet:\n              path: /\n              port: http\n            periodSeconds: 5\n")
    const tcp = fixed(problem("k8s_no_liveness_probe", C1, 17), DEPLOY.replace("- name: http\n              containerPort", "- containerPort"))
    expect(tcp).toContain("          livenessProbe:\n            tcpSocket:\n              port: 8080\n            initialDelaySeconds: 10\n            periodSeconds: 10\n")
    // No port: nothing to probe.
    expect(kubeQuickFixes(problem("k8s_no_readiness_probe", "spec.template.spec.containers[0]", 15), DEPLOY)).toEqual([])
  })

  test("k8s_run_as_root: pod securityContext runAsNonRoot", () => {
    const out = fixed(problem("k8s_run_as_root", "spec.template.spec.securityContext", 13))
    expect(out).toContain("      volumes: []\n      securityContext:\n        runAsNonRoot: true\n")
    const off = DEPLOY.replace("      volumes: []\n", "      securityContext:\n        runAsNonRoot: false\n      volumes: []\n")
    expect(fixed(problem("k8s_run_as_root", "spec.template.spec.securityContext", 13), off)).toContain("        runAsNonRoot: true\n      volumes: []")
  })

  test("k8s_hardening: an empty securityContext becomes the restricted one", () => {
    const out = fixed(problem("k8s_hardening", `${C1}.securityContext`, 24, { fix: "Set securityContext: {allowPrivilegeEscalation: false, …}" }))
    expect(out).toContain(
      "          securityContext:\n            allowPrivilegeEscalation: false\n            readOnlyRootFilesystem: true\n            capabilities:\n              drop:\n                - ALL\n      volumes: []",
    )
  })

  test("k8s_hardening: capabilities.drop gets ALL; pod-level API token", () => {
    const text = DEPLOY.replace("          securityContext: {}\n", "          securityContext:\n            readOnlyRootFilesystem: true\n            capabilities:\n              drop:\n                - NET_RAW\n")
    const out = fixed(problem("k8s_hardening", `${C1}.securityContext`, 24), text)
    expect(out).toContain("            readOnlyRootFilesystem: true\n            capabilities:\n              drop:\n                - NET_RAW\n                - ALL\n            allowPrivilegeEscalation: false\n")
    const token = fixed(problem("k8s_hardening", "spec.template.spec", 13, { message: "pod mounts the API token", fix: "Set automountServiceAccountToken: false" }))
    expect(token).toContain("      volumes: []\n      automountServiceAccountToken: false\n")
  })

  test("k8s_privileged sets the flag false", () => {
    const text = DEPLOY.replace("          securityContext: {}\n", "          securityContext:\n            privileged: true\n")
    expect(fixed(problem("k8s_privileged", `${C1}.securityContext.privileged`, 25), text)).toContain("            privileged: false\n")
    const esc = fixed(problem("k8s_privileged", `${C1}.securityContext.allowPrivilegeEscalation`, 24))
    expect(esc).toContain("          securityContext:\n            allowPrivilegeEscalation: false\n      volumes")
  })

  test("deprecated/removed apiVersions: the version the fix names, in the right document", () => {
    const text = DEPLOY.replace("apiVersion: apps/v1", "apiVersion: extensions/v1beta1")
    const out = fixed(problem("k8s_removed_api", "apiVersion", 7, { fix: "Use apiVersion: apps/v1. Also: spec.selector is required." }), text)
    expect(out).toContain("---\napiVersion: apps/v1\nkind: Deployment")
    expect(out).toContain("# the web app\napiVersion: v1\nkind: ConfigMap")
    expect(replacementApiVersion("Use apiVersion: autoscaling/v2 (behavior moved).")).toBe("autoscaling/v2")
    expect(replacementApiVersion("Use apps/v1")).toBe("apps/v1")
    expect(replacementApiVersion("Use apiVersion: v1.")).toBe("v1")
    expect(kubeQuickFixes(problem("k8s_deprecated_api", "apiVersion", 7, { fix: "Remove it" }), text)).toEqual([])
  })

  test("k8s_pvc_storage_class", () => {
    const pvc = "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\nspec:\n  accessModes: [ReadWriteOnce]\n  resources:\n    requests:\n      storage: 1Gi\n"
    expect(fixed(problem("k8s_pvc_storage_class", "spec", 5), pvc)).toBe(pvc + "  storageClassName: local-path\n")
  })

  test("other problems have no fix", () => {
    expect(kubeQuickFixes(problem("k8s_single_replica", "spec.replicas", 11), DEPLOY)).toEqual([])
    expect(kubeQuickFixes(problem("dk_latest_tag", "", 11), DEPLOY)).toEqual([])
  })
})
