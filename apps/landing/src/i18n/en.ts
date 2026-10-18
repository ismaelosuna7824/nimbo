export const en = {
  meta: {
    title: "Nimbo — Design, deploy and load-test cloud infrastructure on your laptop",
    description:
      "Nimbo is a free desktop app to design cloud infrastructure by connecting nodes, export or import Terraform, deploy to a private local cloud (AWS, Azure, GCP, OCI) and load-test it — without touching a real account.",
    ogImageAlt: "Nimbo mascot next to the headline: Design, deploy and load-test cloud infrastructure on your laptop.",
  },
  a11y: {
    skip: "Skip to content",
    home: "Nimbo home",
    primaryNav: "Main",
    footerNav: "Footer",
    menu: "Menu",
    copied: "Copied to clipboard",
    externalNew: "(opens GitHub)",
  },
  nav: {
    features: "Features",
    how: "How it works",
    agents: "AI agents",
    start: "Get started",
    faq: "FAQ",
    github: "GitHub",
    download: "Download",
    language: "Language",
  },
  os: {
    mac: "macOS",
    windows: "Windows",
    linux: "Linux",
  },
  hero: {
    eyebrow: "A local ADE for cloud infrastructure",
    titleStart: "Design, deploy and load-test cloud infrastructure",
    titleAccent: "on your laptop.",
    sub: "Drag and connect resources from AWS, Azure, Google Cloud and OCI, get clean Terraform out of it, run it in a private local cloud and see how it holds up under traffic. Nothing touches your real account — or your bill.",
    downloadFor: "Download for {os}",
    download: "Download Nimbo",
    downloadDesktop: "Get it for your computer",
    github: "View on GitHub",
    meta: "Free · Runs locally · macOS, Windows, Linux",
  },
  how: {
    eyebrow: "How it works",
    title: "From idea to tested infrastructure in four steps",
    sub: "Design it, run it locally, put it under load, then take the Terraform with you.",
  },
  features: {
    eyebrow: "Features",
    title: "Everything between the whiteboard and production",
    sub: "One app for the parts of cloud work that usually need a real account, a pile of config files and some courage.",
    items: {
      design: {
        eyebrow: "Design by connecting",
        title: "Drag, drop, connect. That's your architecture.",
        body: "Every resource of the AWS, Azure, Google Cloud and OCI Terraform providers — about 5,200 types — is a node on a canvas. Connect two nodes and Nimbo wires them up for you.",
        bullets: [
          "Guided mode: required fields first, sensible defaults and one-click recipes like “SQS → Lambda trigger”",
          "Pro mode: every attribute, raw HCL expressions, meta-arguments and a ⌘K command palette",
          "Per-environment variables, with secrets masked",
        ],
      },
      terraform: {
        eyebrow: "Terraform both ways",
        title: "Export clean Terraform. Or bring yours.",
        body: "Export any workflow as idiomatic, terraform fmt–clean files with a .tfvars per environment. Already have Terraform? Import a whole folder — local modules included — and get the diagram back, with connections derived from references.",
        bullets: [
          "Plain HCL you own: no lock-in, no proprietary format",
          "A .tfvars file per environment",
          "Import a repo folder and finally see its architecture",
        ],
      },
      sandbox: {
        eyebrow: "Private local cloud",
        title: "Deploy to a cloud that lives on your laptop.",
        body: "Each workflow gets its own sandbox in Docker, running the Floci emulators it needs for AWS, Azure, Google Cloud and OCI. Terraform runs inside it, and the canvas shows what's deployed.",
        bullets: [
          "Nothing touches your real cloud account or your bill",
          "One isolated sandbox per workflow",
          "Local URLs and test credentials stay the same across restarts",
        ],
      },
      endpoints: {
        eyebrow: "Endpoints & Try it",
        title: "Every API, queue, bucket and table — ready to use.",
        body: "The Endpoints tab explains each deployed resource in plain language, with copyable URLs and identifiers and snippets you can paste right away.",
        bullets: [
          "Snippets for curl, AWS CLI, JavaScript and Python",
          "Try it: send requests and messages, then read them back",
          "One-click Postman collection export",
        ],
      },
      loadtest: {
        eyebrow: "Load testing",
        title: "Know how it behaves under traffic — before production does.",
        body: "Pick smoke, load, stress, spike or soak — or design your own stages. Watch latency percentiles, errors and a stability score live, find the breaking point and set pass/fail thresholds.",
        bullets: [
          "Live p50 / p95 / p99 latency, error rate and stability score",
          "HTTP endpoints and AWS operations: SQS, S3, DynamoDB, Lambda, SNS, Kinesis, EventBridge, Step Functions",
          "Run history and side-by-side comparison",
        ],
      },
      mcp: {
        eyebrow: "AI agents via MCP",
        title: "Let your AI agent build it with you.",
        body: "Nimbo ships a built-in MCP server. Your agent can create and extend workflows, deploy them to the sandbox, call what it deployed and load-test it — and you watch every change live on the canvas.",
        agentsLabel: "One-click install from Settings for",
        promptsLabel: "Try asking",
        prompts: [
          "Create an HTTP API with a Lambda and a DynamoDB table and load-test it.",
          "Import the Terraform in ~/code/platform and tell me what's in it.",
          "Deploy this workflow and load-test the API: I need p95 under 200 ms at 50 rps.",
        ],
      },
      connections: {
        eyebrow: "Connections",
        title: "Test the services you already have.",
        body: "Save Connections to things that already run elsewhere — any HTTP API, PostgreSQL, MySQL, Redis, MongoDB, or a real AWS account through a profile — and load-test them from the home screen or from any workflow.",
        bullets: [
          "Header, bearer, basic and API-key auth",
          "Secrets stay on your computer and are never shown again",
          "Traffic that leaves the local sandbox always asks for confirmation",
        ],
      },
    },
  },
  modes: {
    eyebrow: "Guided & Pro",
    title: "Made for beginners and pros",
    sub: "Switch modes anytime. Same workflow, same Terraform — just a different amount of detail.",
    guided: {
      title: "Guided",
      body: "New to the cloud? Start here.",
      bullets: [
        "Only the fields you need, with curated defaults",
        "One-click connection recipes",
        "Plain-language explanations of what you deployed",
        "A first-run guide that checks Docker for you",
      ],
    },
    pro: {
      title: "Pro",
      body: "You know Terraform. Go fast.",
      bullets: [
        "Every attribute and raw HCL expressions",
        "Meta-arguments and code editing",
        "⌘K command palette",
        "Import existing folders, export clean code",
      ],
    },
  },
  start: {
    eyebrow: "Get started",
    title: "Get started in one command",
    sub: "One script checks your computer, installs what's missing (asking first, from official sources) and builds the app.",
    unix: "macOS / Linux",
    windows: "Windows (PowerShell)",
    copy: "Copy",
    copied: "Copied",
    or: "Prefer a ready-made app?",
    download: "Download the app",
    docker:
      "Docker isn't needed to build. On first launch, Nimbo checks Docker for your OS and the sandbox images, explains anything missing in plain language and fixes it with one click where it can.",
    docs: "Setup guide",
  },
  faq: {
    eyebrow: "FAQ",
    title: "Questions, answered",
    items: [
      {
        id: "free",
        q: "Is it free?",
        a: "Yes. Nimbo is free and runs on your own computer — no account, no sign-up, no subscription. The source code is on GitHub.",
      },
      {
        id: "aws-account",
        q: "Does it touch my AWS account?",
        a: "No. Deployments go to a private sandbox on your machine, powered by Floci emulators in Docker. A real account is only used if you explicitly save a Connection to it, and any traffic that leaves the local sandbox asks for your confirmation first.",
      },
      {
        id: "docker",
        q: "Do I need Docker?",
        a: "Yes, to run the local cloud: Docker Desktop, OrbStack, Colima, Rancher Desktop, Docker Engine on Linux, or Podman with the docker CLI all work. You don't need it to build the app — the first-run guide checks Docker for your OS and helps you fix what's missing. No host Terraform needed either.",
      },
      {
        id: "clouds",
        q: "Which clouds and services are supported?",
        a: "For design and Terraform: every resource of the aws, azurerm, google and oci providers — about 5,200 types. For local deploys: what the Floci emulators support for AWS, Azure, Google Cloud and OCI. Nimbo tells you which resources can run locally.",
      },
      {
        id: "export",
        q: "Can I export my Terraform?",
        a: "Yes. Every workflow exports as idiomatic, terraform fmt–clean files with a .tfvars per environment. It's plain Terraform you can commit and apply anywhere. You can also import an existing folder and get the diagram back.",
      },
      {
        id: "agents",
        q: "How do AI agents connect?",
        a: "Through Nimbo's built-in MCP server. Open Settings → AI agents and click Install for Claude Code, Claude Desktop, Cursor, Codex, VS Code, Windsurf or Gemini CLI — or register `nimbo mcp` manually. The agent's edits show up live in the app.",
      },
      {
        id: "data",
        q: "Is my data sent anywhere?",
        a: "The app is local-first: workflows, Connections and secrets stay on your computer (in ~/.nimbo). This website uses Google Analytics for Firebase only if you accept the cookie banner, and you can change your choice anytime from “Cookie settings” in the footer.",
      },
    ],
  },
  cta: {
    title: "Your next architecture is one drag away.",
    sub: "Free, local and ready when you are.",
  },
  footer: {
    tagline: "A local ADE for cloud infrastructure.",
    product: "Product",
    docs: "Docs",
    docsReadme: "README",
    docsSetup: "Setup",
    docsMcp: "MCP server",
    docsArchitecture: "Architecture",
    releases: "Releases",
    poweredBy: "Local cloud powered by",
    poweredByName: "Floci emulators",
    cookies: "Cookie settings",
    license: "Source code on GitHub. Terraform is a trademark of HashiCorp; AWS, Azure, Google Cloud and OCI are trademarks of their owners.",
  },
  consent: {
    title: "Analytics cookies",
    text: "We'd like to use Google Analytics for Firebase to understand how this website is used. It only runs if you accept. The Nimbo app itself has no analytics.",
    accept: "Accept",
    decline: "Decline",
  },
  notFound: {
    title: "Page not found",
    body: "This page drifted away like a cloud. Let's get you back.",
    home: "Back to home",
  },
};

export type Strings = typeof en;
