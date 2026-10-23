import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { K8S_ICON_IDS, isK8sIconId, k8sIcon, k8sIconFor, k8sIconIdForKind, k8sIconSrc } from "./index";

const ASSETS = join(import.meta.dir, "assets");

describe("kubernetes icon registry", () => {
  test("covers the object kinds of the Kubernetes workspace contract", () => {
    const required = "deploy sts ds job cronjob svc ing cm secret pvc pv sc hpa pdb netpol sa role ns pod";
    for (const id of required.split(" ")) expect(isK8sIconId(id)).toBe(true);
  });

  test("every id has exactly one optimised asset", () => {
    const files = readdirSync(ASSETS).sort();
    expect(files).toEqual(K8S_ICON_IDS.map((id) => `${id}.svg`).sort());
    for (const file of files) {
      const svg = readFileSync(join(ASSETS, file), "utf8");
      expect(svg.startsWith("<svg")).toBe(true);
      expect(svg).toContain("viewBox=");
      expect(svg).not.toMatch(/<script|<foreignObject|xlink:href="http|href="http|on[a-z]+="|inkscape|sodipodi|style="/i);
      // Inlined copies (Architecture SVG export) must not collide: no ids at all.
      expect(svg).not.toMatch(/\bid="/);
      expect(statSync(join(ASSETS, file)).size).toBeLessThan(6_000);
    }
  });

  test("kinds resolve to their icon, the label is the kind", () => {
    expect(k8sIconIdForKind("Deployment")).toBe("deploy");
    expect(k8sIconIdForKind("StatefulSet")).toBe("sts");
    expect(k8sIconIdForKind("HorizontalPodAutoscaler")).toBe("hpa");
    expect(k8sIconIdForKind("ClusterRoleBinding")).toBe("crb");
    expect(k8sIconIdForKind("HTTPRoute")).toBe("httproute");
    expect(k8sIconFor("Service")).toMatchObject({ id: "svc", label: "Service", short: "svc" });
    expect(k8sIconFor("IngressClass")).toMatchObject({ id: "ing", label: "IngressClass" });
    expect(k8sIconFor("Service").src).toBe(k8sIconSrc("svc"));
  });

  test("unknown kinds are custom resources", () => {
    expect(k8sIconFor("Certificate")).toMatchObject({ id: "crd", label: "Certificate" });
    expect(k8sIconIdForKind("constructor")).toBe("crd");
    expect(k8sIcon("nope").id).toBe("crd");
    expect(k8sIconFor("").label).toBe("Custom resource");
  });
});
