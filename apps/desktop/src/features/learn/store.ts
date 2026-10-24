import { create } from "zustand"
import { useQuery } from "@tanstack/react-query"
import { CLOUD_META } from "@/components/cloud-meta"
import { api } from "@/lib/api/client"
import type { LearnProviderId } from "@/lib/api/types"

export type LearnTab = "roadmap" | "challenges"

interface LearnState {
  /** The full-page Learn view covers home / the editor while open. */
  viewOpen: boolean
  provider: LearnProviderId
  tab: LearnTab
  /** The challenge page shown over the provider page, null for none. */
  challengeId: string | null

  openView: (to?: { provider?: LearnProviderId; tab?: LearnTab; challengeId?: string | null }) => void
  closeView: () => void
  setProvider: (provider: LearnProviderId) => void
  setTab: (tab: LearnTab) => void
  openChallenge: (id: string | null) => void
}

export const useLearnStore = create<LearnState>((set) => ({
  viewOpen: false,
  provider: "aws",
  tab: "roadmap",
  challengeId: null,

  openView: (to = {}) =>
    set((s) => ({
      viewOpen: true,
      provider: to.provider ?? s.provider,
      tab: to.tab ?? s.tab,
      challengeId: to.challengeId === undefined ? s.challengeId : to.challengeId,
    })),
  closeView: () => set({ viewOpen: false }),
  setProvider: (provider) => set({ provider, challengeId: null }),
  setTab: (tab) => set({ tab, challengeId: null }),
  openChallenge: (challengeId) => set({ challengeId }),
}))

export const learnKeys = {
  all: ["learn"] as const,
  overview: ["learn", "overview"] as const,
  roadmap: (p: LearnProviderId) => ["learn", "roadmap", p] as const,
  challenges: ["learn", "challenges"] as const,
  challenge: (id: string) => ["learn", "challenge", id] as const,
}

/** Every challenge with the user's progress (roadmaps link across providers). */
export function useAllChallenges() {
  return useQuery({ queryKey: learnKeys.challenges, queryFn: () => api.learnChallenges() })
}

export const PROVIDERS: LearnProviderId[] = ["aws", "azure", "gcp", "oci", "docker", "kubernetes", "global"]

export const PROVIDER_META: Record<LearnProviderId, { label: string; dot: string; blurb: string }> = {
  aws: { label: CLOUD_META.aws.label, dot: CLOUD_META.aws.dot, blurb: "Terraform on AWS, deployed to the local emulator." },
  azure: { label: CLOUD_META.azure.label, dot: CLOUD_META.azure.dot, blurb: "Terraform designs for Azure, validated in Nimbo." },
  gcp: { label: CLOUD_META.gcp.label, dot: CLOUD_META.gcp.dot, blurb: "Terraform designs for Google Cloud, validated in Nimbo." },
  oci: { label: CLOUD_META.oci.label, dot: CLOUD_META.oci.dot, blurb: "Terraform designs for Oracle Cloud, validated in Nimbo." },
  docker: { label: "Docker", dot: "bg-blue-500", blurb: "Compose projects running on your machine." },
  kubernetes: { label: "Kubernetes", dot: "bg-indigo-500", blurb: "Manifests, Kustomize and Helm on a local cluster." },
  global: { label: "Global", dot: "bg-fuchsia-500", blurb: "Provider-agnostic problems, with how to solve them on each provider." },
}

export const LEVELS = ["beginner", "intermediate", "advanced", "expert"] as const

export const LEVEL_LABEL: Record<(typeof LEVELS)[number], string> = {
  beginner: "Beginner",
  intermediate: "Intermediate",
  advanced: "Advanced",
  expert: "Expert",
}

export const RUNTIME_LABEL = {
  local: { label: "Runs locally", hint: "Deployed on your machine (Docker or a local Kubernetes cluster)." },
  emulated: { label: "Emulated cloud", hint: "Deployed to the local AWS emulator (Floci)." },
  design: { label: "Design & validate", hint: "Graded on the design: validation, rules and structure — nothing is deployed." },
} as const
