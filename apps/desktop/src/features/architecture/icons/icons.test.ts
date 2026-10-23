import { describe, expect, test } from "bun:test";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  ARCH_ICON_SLUGS,
  actorIcon,
  archIconFor,
  archIconSrc,
  groupIcon,
  isPlumbing,
  providerLogo,
  providerOfType,
  type ArchProvider,
} from "./index";

const CATALOG = join(import.meta.dir, "../../../../../../backend/internal/catalog");
const ASSETS = join(import.meta.dir, "assets");

/** Every resource/data type of the curated catalog starters and the templates. */
function catalogTypes(): string[] {
  const curated = JSON.parse(readFileSync(join(CATALOG, "curated/resources.json"), "utf8")) as {
    resources: Record<string, unknown>;
    data: Record<string, unknown>;
  };
  const types = new Set<string>([
    ...Object.keys(curated.resources),
    ...Object.keys(curated.data).map((t) => `data.${t}`),
  ]);
  const templates = join(CATALOG, "templates");
  for (const dir of readdirSync(templates, { withFileTypes: true })) {
    if (!dir.isDirectory()) continue;
    for (const file of readdirSync(join(templates, dir.name))) {
      if (!file.endsWith(".tf")) continue;
      const hcl = readFileSync(join(templates, dir.name, file), "utf8");
      for (const m of hcl.matchAll(/^(resource|data)\s+"([a-z0-9_]+)"/gm)) {
        types.add(m[1] === "data" ? `data.${m[2]}` : m[2]);
      }
    }
  }
  return [...types].sort();
}

describe("architecture icon registry", () => {
  const types = catalogTypes();

  test("catalog has types for every provider", () => {
    const providers = new Set(types.map(providerOfType).filter(Boolean));
    expect([...providers].sort()).toEqual(["aws", "azure", "gcp", "oci"]);
  });

  test("every catalog starter/template type resolves to a service-specific icon", () => {
    const generic: string[] = [];
    for (const t of types) {
      const provider = providerOfType(t);
      if (!provider) {
        // Utility providers (archive, random…) never get a card.
        expect(isPlumbing("aws", t)).toBe(true);
        continue;
      }
      if (archIconFor(provider, t).generic) generic.push(t);
    }
    expect(generic).toEqual([]);
  });

  test("data sources resolve like their resource", () => {
    expect(archIconFor("aws", "data.aws_s3_bucket").src).toBe(archIconFor("aws", "aws_s3_bucket").src);
    expect(archIconFor("gcp", "data.google_storage_bucket").src).toBe(
      archIconFor("gcp", "google_storage_bucket").src,
    );
  });

  test("unknown types fall back to a category or provider generic", () => {
    const cases: [ArchProvider, string][] = [
      ["aws", "aws_lambda_something_new"],
      ["aws", "aws_totally_unknown"],
      ["azure", "azurerm_totally_unknown"],
      ["gcp", "google_totally_unknown"],
      ["oci", "oci_totally_unknown"],
    ];
    for (const [provider, t] of cases) {
      const icon = archIconFor(provider, t);
      expect(icon.src).toBeTruthy();
      expect(existsSync(fileURLToPath(icon.src))).toBe(true);
    }
    expect(archIconFor("aws", "aws_lambda_something_new").generic).toBe(false);
    expect(archIconFor("aws", "aws_totally_unknown").generic).toBe(true);
  });

  test("non-cloud technology types get their technology icon, cloud services keep theirs", () => {
    const k8s = archIconFor("aws", "kubernetes_deployment");
    expect(k8s).toMatchObject({ service: "Kubernetes", category: "containers", generic: false });
    expect(k8s.src).toMatch(/tech-icons\/assets\/kubernetes\.svg$/);
    expect(existsSync(fileURLToPath(k8s.src))).toBe(true);
    expect(archIconFor("aws", "data.kubernetes_namespace").src).toBe(k8s.src);
    expect(archIconFor("gcp", "kubectl_manifest").src).toBe(k8s.src);
    expect(archIconFor("aws", "helm_release").src).toMatch(/tech-icons\/assets\/helm\.svg$/);
    expect(archIconFor("azure", "docker_container").src).toMatch(/tech-icons\/assets\/docker\.svg$/);
    expect(archIconFor("aws", "vault_kv_secret_v2")).toMatchObject({ service: "HashiCorp Vault", category: "security" });
    expect(archIconFor("aws", "postgresql_database").src).toMatch(/tech-icons\/assets\/postgres\.svg$/);
    expect(archIconFor("aws", "aws_eks_cluster").src).toMatch(/architecture\/icons\/assets\/aws-eks\.svg$/);
    expect(archIconFor("azure", "azurerm_kubernetes_cluster").src).toMatch(/architecture\/icons\/assets\/azure-aks\.svg$/);
    expect(archIconFor("aws", "aws_db_instance").src).not.toContain("tech-icons");
  });

  test("plumbing folds helpers but keeps primary services", () => {
    expect(isPlumbing("aws", "aws_iam_role_policy_attachment")).toBe(true);
    expect(isPlumbing("aws", "aws_lambda_permission")).toBe(true);
    expect(isPlumbing("aws", "aws_route_table_association")).toBe(true);
    expect(isPlumbing("aws", "data.archive_file")).toBe(true);
    expect(isPlumbing("aws", "random_id")).toBe(true);
    expect(isPlumbing("aws", "aws_lambda_function")).toBe(false);
    expect(isPlumbing("aws", "aws_sqs_queue")).toBe(false);
    expect(isPlumbing("azure", "azurerm_storage_queue")).toBe(false);
  });

  test("every mapped src file exists and every asset is mapped", () => {
    for (const slug of ARCH_ICON_SLUGS) {
      expect(existsSync(fileURLToPath(archIconSrc(slug)))).toBe(true);
    }
    const files = readdirSync(ASSETS).map((f) => f.replace(/\.svg$/, ""));
    expect(files.sort()).toEqual([...ARCH_ICON_SLUGS].sort());
  });

  test("assets are clean SVGs with a viewBox", () => {
    for (const slug of ARCH_ICON_SLUGS) {
      const svg = readFileSync(join(ASSETS, `${slug}.svg`), "utf8");
      expect(svg.startsWith("<svg")).toBe(true);
      expect(svg).toContain("viewBox=");
      expect(svg).not.toContain("<script");
    }
  });

  test("logos, groups and actors resolve to bundled files", () => {
    const providers: ArchProvider[] = ["aws", "azure", "gcp", "oci"];
    for (const p of providers) {
      expect(existsSync(fileURLToPath(providerLogo(p)))).toBe(true);
      for (const kind of ["vpc", "subnet", "account"] as const) {
        const src = groupIcon(p, kind);
        expect(src && existsSync(fileURLToPath(src))).toBe(true);
      }
      expect(groupIcon(p, "az")).toBeUndefined();
    }
    for (const kind of ["users", "internet", "service"] as const) {
      expect(existsSync(fileURLToPath(actorIcon(kind).src))).toBe(true);
    }
  });
});
