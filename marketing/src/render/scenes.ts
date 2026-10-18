/**
 * The promo storyboard. All times are video milliseconds; `speed` is how fast
 * the landing demo's own timeline runs during the scene (1 = as on the site),
 * `preroll` fast-forwards the demo before the video starts so the scene opens
 * mid-demo. Captions accept inline <em> for the accent color.
 */
export type DemoKey = "hero" | "sandbox" | "endpoints" | "loadtest" | "mcp" | "terraform";

export interface DemoScene {
  id: string;
  demo: DemoKey;
  start: number;
  end: number;
  speed: number;
  preroll?: number;
  /** The demo moves a fake cursor, so it must not be scaled to fit (cursor math assumes scale 1); it is cropped instead. */
  cursor: boolean;
  kicker: string;
  title: string;
  sub: string;
}

export const FPS = 30;

export const HOOK = { start: 0, end: 3200 };

export const SCENES: readonly DemoScene[] = [
  {
    id: "design",
    demo: "hero",
    start: 3200,
    end: 7700,
    speed: 1.5,
    cursor: true,
    kicker: "01 · Design",
    title: "Drag, drop and <em>connect</em> your cloud",
    sub: "AWS, Azure, Google Cloud and Oracle Cloud resources on one canvas",
  },
  {
    id: "deploy",
    demo: "sandbox",
    start: 7700,
    end: 13000,
    speed: 1.25,
    cursor: false,
    kicker: "02 · Deploy",
    title: "One click into a <em>private local cloud</em>",
    sub: "Floci emulators in Docker · AWS, Azure, GCP, OCI · <b>$0 cloud bill</b>",
  },
  {
    id: "try",
    demo: "endpoints",
    start: 13000,
    end: 18000,
    speed: 1.2,
    preroll: 1500,
    cursor: true,
    kicker: "03 · Try it",
    title: "Call your API <em>right away</em>",
    sub: "<code>POST /items</code> → <b>201 Created</b>, straight from the Endpoints tab",
  },
  {
    id: "loadtest",
    demo: "loadtest",
    start: 18000,
    end: 24000,
    speed: 1.75,
    cursor: false,
    kicker: "04 · Load test",
    title: "Load-test it live. Find the <em>breaking point</em>",
    sub: "Throughput, p95 latency and errors as they happen",
  },
  {
    id: "agent",
    demo: "mcp",
    start: 24000,
    end: 30000,
    speed: 1.55,
    cursor: false,
    kicker: "05 · AI agents",
    title: "Or let your <em>AI agent</em> build it",
    sub: "Nimbo's MCP server: design, deploy and test from one prompt",
  },
  {
    id: "terraform",
    demo: "terraform",
    start: 30000,
    end: 36500,
    speed: 1.65,
    cursor: false,
    kicker: "06 · Terraform",
    title: "Terraform <em>in</em>, Terraform <em>out</em>",
    sub: "Import an existing folder, export clean <code>terraform fmt</code> HCL",
  },
];

export const CTA = { start: 36500, end: 41000 };

export const DURATION = CTA.end;

/** Video time of the poster frame: the hook card, fully revealed. */
export const POSTER_AT = 2000;
