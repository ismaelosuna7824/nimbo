import type { ChallengeSummary } from "../../src/lib/api/types"
import { expect, test } from "../support/fixtures"

// Challenges that still need code or file edits carry a "Requires code" badge in the Learn catalog and on their page;
// the rest (solvable with the canvas and inspector) don't.

const REQUIRES_CODE = [
  "aws-sqs-dead-letter-queue",
  "azure-landing-zone-governance",
  "gcp-gke-hardened-cluster",
  "gcp-org-policy-guardrails",
  "oci-lb-instance-pool-autoscaling",
  "docker-cache-friendly-dockerfile",
  "docker-multi-stage-nonroot",
  "docker-observability",
  "docker-build-secrets-supply-chain",
  "docker-lb-health-checks",
  "kubernetes-helm-values-per-environment",
]

test("marks the challenges that require code", async ({ app, api, page }) => {
  const all = await api.request<ChallengeSummary[]>("GET", "/learn/challenges")
  expect(all).toHaveLength(78)
  expect(all.every((c) => typeof c.solvableWithoutCode === "boolean")).toBe(true)
  expect(all.filter((c) => c.solvableWithoutCode === false).map((c) => c.id).sort()).toEqual([...REQUIRES_CODE].sort())

  await app.gotoHome()
  await page.getByRole("button", { name: "Open Learn" }).click()
  const providers = page.getByRole("navigation", { name: "Providers" })
  for (const provider of [...new Set(all.map((c) => c.provider))]) {
    // The catalog lists one provider at a time.
    await providers.getByRole("button", { name: PROVIDER_NAME[provider] }).click()
    await page.getByRole("tab", { name: /^Challenges/ }).click()
    for (const c of all.filter((x) => x.provider === provider)) {
      const card = page.getByRole("button", { name: new RegExp(`^${escape(c.title)}`) }).first()
      await expect(card).toBeVisible()
      await expect(card.getByTestId("challenge-requires-code")).toHaveCount(c.solvableWithoutCode ? 0 : 1)
    }
  }

  // The challenge page header too.
  const helm = all.find((c) => c.id === "kubernetes-helm-values-per-environment")!
  await providers.getByRole("button", { name: PROVIDER_NAME.kubernetes }).click()
  await page.getByRole("tab", { name: /^Challenges/ }).click()
  await page.getByRole("button", { name: new RegExp(`^${escape(helm.title)}`) }).first().click()
  await expect(page.getByTestId("challenge-requires-code").first()).toBeVisible()
})

function escape(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}

const PROVIDER_NAME: Record<string, RegExp> = {
  aws: /AWS/,
  azure: /Azure/,
  gcp: /Google|GCP/,
  oci: /Oracle|OCI/,
  docker: /Docker/,
  kubernetes: /Kubernetes/,
  global: /Global/,
}
