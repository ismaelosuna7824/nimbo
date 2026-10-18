/**
 * Animated product demos. Every component takes `{ lang, class? }`, renders
 * its own window frame (no section heading) and starts when scrolled into
 * view. `titleKey` indexes `demoStrings(lang).titles`.
 */
import AgentMCP from "./AgentMCP.astro";
import ConnectRecipe from "./ConnectRecipe.astro";
import Connections from "./Connections.astro";
import EndpointsTry from "./EndpointsTry.astro";
import FlowSteps from "./FlowSteps.astro";
import GuidedPro from "./GuidedPro.astro";
import HeroCanvas from "./HeroCanvas.astro";
import LoadTestLive from "./LoadTestLive.astro";
import LocalCloud from "./LocalCloud.astro";
import TerraformBothWays from "./TerraformBothWays.astro";

export const demos = [
  { id: "hero", component: HeroCanvas, titleKey: "hero" },
  { id: "flow", component: FlowSteps, titleKey: "flow" },
  { id: "design", component: ConnectRecipe, titleKey: "design" },
  { id: "terraform", component: TerraformBothWays, titleKey: "terraform" },
  { id: "sandbox", component: LocalCloud, titleKey: "sandbox" },
  { id: "endpoints", component: EndpointsTry, titleKey: "endpoints" },
  { id: "loadtest", component: LoadTestLive, titleKey: "loadtest" },
  { id: "mcp", component: AgentMCP, titleKey: "mcp" },
  { id: "connections", component: Connections, titleKey: "connections" },
  { id: "modes", component: GuidedPro, titleKey: "modes" },
] as const;

export type DemoId = (typeof demos)[number]["id"];

export function getDemo(id: DemoId): (typeof demos)[number] {
  return demos.find((d) => d.id === id)!;
}

export { demoStrings } from "./lib/strings";
