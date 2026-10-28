/** Strings for the animated product demos (src/components/demos). */
export const demosEn = {
  /** Short accessible titles, indexed by demo id (`titleKey`). */
  titles: {
    hero: "Nimbo canvas preview",
    flow: "From design to Terraform",
    design: "One-click connection recipes",
    terraform: "Terraform both ways",
    sandbox: "A private local cloud",
    endpoints: "Endpoints and Try it",
    loadtest: "Live load testing",
    mcp: "AI agents through MCP",
    connections: "Connections to deployed services",
    modes: "Guided and Pro modes",
    architecture: "Build and Architecture views",
    docker: "Docker workspaces",
    kubernetes: "Kubernetes workspaces",
    readiness: "Production readiness",
    learn: "Learn roadmaps and challenges",
    plan: "Terraform plan on the canvas",
    traffic: "Live traffic and failure tests",
    compliance: "CIS compliance pre-audit",
    cicd: "CI/CD pipeline export",
  },
  common: {
    local: "Local",
    deployed: "Deployed",
    module: "Module",
    deploy: "Deploy",
    test: "Test",
    guided: "Guided",
    pro: "Pro",
    sandboxOff: "Sandbox off",
    sandboxStarting: "Starting…",
    sandboxRunning: "Sandbox running",
    copy: "Copy",
    copied: "Copied",
    send: "Send",
    passed: "Passed",
    running: "Running",
  },
  hero: {
    label:
      "Animated preview of the Nimbo app: an API Gateway, a Lambda function and a DynamoDB table are dragged from the resource palette onto the canvas and connected. Deploy is pressed, the local sandbox starts, each resource is marked Deployed and a notification shows the API's local URL.",
    windowTitle: "Nimbo",
    workflow: "Orders API",
    search: "Search AWS…",
    toastTitle: "Deployed — your API is live",
    endpoints: "Open Endpoints",
    loadTest: "Load test",
  },
  flow: {
    label:
      "Four steps light up one after another as a beam travels across them: Design the infrastructure on a canvas, run it locally in a private sandbox, test it with endpoints and load tests, and ship clean Terraform.",
    steps: [
      { title: "Design", text: "Drag resources, connect them, get the wiring right.", chip: "drag · connect · ⌘K" },
      { title: "Run locally", text: "Deploy into a private cloud sandbox on your machine.", chip: "Floci · Docker" },
      { title: "Test", text: "Call every endpoint and load-test it with live charts.", chip: "p95 12 ms · passed" },
      { title: "Ship Terraform", text: "Export formatted code and tfvars for each environment.", chip: "main.tf · dev.tfvars" },
    ],
  },
  design: {
    label:
      "Animated canvas: a line is dragged from an SQS queue to a Lambda function. A recipe popover suggests triggering the function from the queue; choosing it adds an event source mapping and an IAM policy, wired up automatically.",
    windowTitle: "Guided mode · connection recipes",
    queue: "Jobs queue",
    worker: "Worker",
    popTitle: "Connect SQS queue → Lambda function",
    recommended: "Recommended",
    options: ["Trigger the function from the queue", "Let the function send messages", "Only reference the queue URL"],
    optionHints: ["Adds an event source mapping and the IAM permissions", "Grants sqs:SendMessage and passes the URL", "Sets an environment variable"],
    mapping: "Event source mapping",
    policy: "Queue access policy",
    toast: "Recipe applied · 2 resources, 3 connections",
  },
  terraform: {
    label:
      "Animated split view. A folder with environments and a local module is imported and becomes a diagram with a module node. Then a queue is added on the canvas and clean Terraform code types out next to the diagram, formatted with terraform fmt.",
    windowTitle: "Terraform · both ways",
    import: "Import",
    export: "Export",
    importing: "Reading HCL…",
    imported: "Imported 2 environments · 1 module",
    fmt: "terraform fmt ✓",
    network: "Network",
    uploads: "Uploads",
    thumbs: "Thumbnails",
  },
  sandbox: {
    label:
      "Animated view of your machine: Docker runs private Floci emulators for AWS, Azure, Google Cloud and Oracle Cloud. A Terraform log streams resource creation, the resources switch to Deployed, and a stamp shows the real cloud bill stays at $0.00.",
    windowTitle: "Local sandbox · orders-api",
    machine: "Your machine",
    docker: "Docker",
    log: "terraform apply",
    stampTop: "Real cloud bill",
  },
  endpoints: {
    label:
      "Animated Endpoints tab: the URL of a deployed HTTP API is copied, a POST request to /items is built and sent with Try it, and the response shows 201 Created with JSON and latency. Then a message is sent to an SQS queue and confirmed.",
    windowTitle: "Endpoints",
    tabs: ["Deploy", "Endpoints", "Load test", "Terraform"],
    api: "HTTP API",
    queue: "Jobs queue",
    tryIt: "Try it",
    snippets: ["curl", "AWS CLI", "SDK", "Postman"],
    response: "Response",
    created: "201 Created",
    message: "Message body",
    sendMessage: "Send message",
    sent: "Message sent",
  },
  loadtest: {
    label:
      "Animated load test results: request counters and latency figures count up while a live chart shows target versus achieved requests per second and p50, p95 and p99 latency. As traffic ramps past about 350 requests per second, latency jumps and a breaking point marker appears. The run ends with a Passed verdict and its thresholds.",
    windowTitle: "Load test · stress · orders-api",
    name: "Stress · POST /items",
    kpis: { requests: "Requests", rps: "Peak req/s", p95: "p95 latency", errors: "Error rate", stability: "Stability" },
    stable: "Stable",
    throughput: "Throughput",
    latency: "Latency",
    target: "Target",
    achieved: "Achieved",
    breaking: "Breaking point ≈ 350 req/s",
    criteria: "Pass criteria",
    thresholds: ["p95 < 500 ms", "error rate < 1%", "checks > 99%"],
  },
  mcp: {
    label:
      "Animated chat with an AI agent connected to Nimbo through MCP. The user asks for an HTTP API with a Lambda and a DynamoDB table, deployed and smoke-tested. Tool calls appear one by one while the Nimbo canvas fills in, deploys, and finishes with a passed test at p95 12 ms.",
    windowTitle: "AI agent · Nimbo MCP",
    agent: "Your AI agent",
    connected: "nimbo MCP connected",
    prompt: "Create an HTTP API with a Lambda and a DynamoDB table, deploy it and run a smoke test",
    reply: "Done — the API is live in your sandbox and the smoke test passed.",
    canvas: "Nimbo · orders-api",
    result: "Passed · p95 12 ms",
    placeholder: "Message your agent…",
  },
  connections: {
    label:
      "Animated Connections screen with saved targets for PostgreSQL, Redis, an HTTP API and an AWS account. Test connection reports PostgreSQL 16 in 4 ms. Before load-testing the staging API, a confirmation dialog asks whether to send real traffic to staging-api.example.com.",
    windowTitle: "Connections",
    test: "Test connection",
    loadTest: "Load test",
    pgOk: "PostgreSQL 16 · 4 ms",
    redisOk: "Redis 7.4 · 1 ms",
    real: "Real",
    awsNote: "Asks before every run",
    dialogTitle: "Send real traffic to staging-api.example.com?",
    dialogText: "This load test hits a real service, not your sandbox. Only continue if you're allowed to test it.",
    cancel: "Cancel",
    confirm: "Send traffic",
    httpOk: "Smoke passed · p95 48 ms",
    testing: "Testing…",
    smoke: "Running smoke test…",
    target: "Target",
    traffic: "Traffic",
    trafficValue: "≈ 600 requests over 1 min",
  },
  architecture: {
    label:
      "The Orders API workflow in Nimbo's two views. Build shows the editable canvas of Terraform resources (an HTTP API with its stage, route and integration, a Lambda function, a DynamoDB table, an IAM role and policies and a log group), each card with its Local status. Architecture draws the same design as a cloud architecture diagram with official AWS icons: users reach an API Gateway, which calls a Lambda function that reads and writes a DynamoDB table, with CloudWatch Logs and an IAM role, inside AWS Cloud and the us-east-1 region, with a legend.",
    windowTitle: "Orders API",
    build: "Build",
    architecture: "Architecture",
  },
  docker: {
    label:
      "A Node.js API with PostgreSQL and Redis from a compose file, running in Nimbo with monitoring added: service cards with their technology icons, a green healthy dot, live CPU and memory, depends_on edges and named volumes, and the api's inspector grouped into Running, Image & build, Ports & networking, Environment and Volumes & storage. Then a passed load test at 50 requests per second: 4,000 requests, p95 3.4 ms, no errors, and a verdict that says no container was the bottleneck, the busiest one, api, peaking at 11% of its CPU limit.",
    windowTitle: "Notes API · compose.yaml",
    running: "Running",
    loadTest: "Load test",
  },
  kubernetes: {
    label:
      "A Kubernetes app running on its local k3s cluster in Nimbo: in the shop namespace an Ingress routes to frontend and backend Services and Deployments scaled by HorizontalPodAutoscalers, with a Redis Deployment, ready dots and live CPU. Then the Convert to Kubernetes dialog turning a compose project into a Helm chart sized from a load test, with autoscaling in prod and the mapping from each compose setting to Kubernetes objects.",
    windowTitle: "Shop on Kubernetes",
    cluster: "Local cluster",
    convert: "Compose → K8s",
  },
  readiness: {
    label:
      "The Readiness tab of a Docker workspace in Nimbo: a score of 85 out of 100 that needs work, with reliability, performance, resilience, monitoring, security and hygiene scores, a confidence of 78% and a row per service with its rules, health, resources, monitoring and load. Resilience is 0 because a failure test failed. Then the capacity planner for 100 requests per second: replicas, vCPU and memory per service from what the load test measured, and the cheapest monthly compute across the clouds.",
    windowTitle: "Notes API",
    score: "Readiness",
    capacity: "Capacity",
  },
  learn: {
    label:
      "Nimbo's Learn page: the Docker roadmap from beginner to expert with its steps, linked challenges and official docs. Then a Docker challenge, An API and its PostgreSQL database: the problem, the requirements and the seven goals Nimbo grades, from a valid compose project to a stack that deploys.",
    windowTitle: "Learn · Docker",
    roadmap: "Roadmap",
    challenge: "Challenge",
  },
  plan: {
    label:
      "A deployed AWS workflow after a Terraform plan, shown on the canvas: a plus marker on a new SQS queue, a tilde on the Lambda function and an IAM policy that change in place, a replace marker on the DynamoDB table, and a legend reading plus 1, tilde 2, replace 1, minus 1. Then the table's inspector with its planned changes: replace, because hash_key changes from id to pk, which forces replacement, followed by every attribute that changes.",
    windowTitle: "Orders API · dev",
    canvas: "Canvas",
    diff: "Diff",
  },
  traffic: {
    label:
      "A Docker workspace under a load test in Nimbo: the api card shows its live rate and latency, 50 requests per second at p95 4.8 ms, while a panel at the bottom follows the run. Then a failure test that took the Redis cache down during the test: the cache card is marked Failed and the api shows 40 requests per second, p95 1.77 s and 18.3% errors, with the verdict Failed.",
    windowTitle: "Notes API · load test",
    live: "Live",
    failure: "Failure test",
  },
  compliance: {
    label:
      "The Compliance tab of a Docker workspace in Nimbo: the CIS Docker Benchmark v1.8.0 checked against the design, with 22 of 27 automatic controls met, 5 violated and 24 manual, marked as a pre-audit, not a certification. The violated controls (capabilities, read-only root filesystem, host interface, no-new-privileges, PIDs limit) list the services they affect, highlighted on the canvas. Then the CIS Kubernetes Benchmark v2.0.1 for a Kubernetes workspace: 12 of 17 automatic controls met, with pod security findings on the Deployments.",
    windowTitle: "Compliance · CIS",
    docker: "Docker",
    kubernetes: "Kubernetes",
  },
  cicd: {
    label:
      "Nimbo's export dialog with a CI/CD pipeline. For a Terraform workflow with dev and prod environments: a root per environment and a service module, plus a GitHub Actions workflow that checks every push, plans each environment on pull requests and applies from the default branch with production last, behind required reviewers, and a smoke check after each deploy. Then a Docker project exported with GitLab CI: images built and pushed to the GitLab registry, deploys over SSH, one environment after the other with a manual start for production.",
    windowTitle: "Export · CI/CD pipeline",
    github: "GitHub Actions",
    gitlab: "GitLab CI",
  },
  modes: {
    label:
      "Animated inspector for a Lambda function switching from Guided mode, with three simple fields and helpful hints, to Pro mode, which shows every attribute, an expression written in HCL and the generated code.",
    windowTitle: "Inspector · aws_lambda_function.api",
    fields: { name: "Name", runtime: "Runtime", memory: "Memory" },
    hints: {
      name: "How this function is called in your cloud.",
      runtime: "The language your code runs on.",
      memory: "More memory also means more CPU.",
    },
    tip: "Recommended connections are applied for you.",
    expr: "Expression",
    hcl: "HCL",
    meta: "Meta-arguments",
  },
};

export type DemoStrings = typeof demosEn;
