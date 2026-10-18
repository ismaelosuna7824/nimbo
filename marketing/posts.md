# Nimbo launch posts

Video: `out/nimbo-promo-1080x1350.mp4` for LinkedIn (4:5), `out/nimbo-promo-1920x1080.mp4` for X (16:9),
`out/nimbo-promo-1080x1080.mp4` where a square fits better. Captions are burned in; the video is silent.
Regenerate with `bun run render` in `marketing/`.

Links: https://github.com/ismaelosuna7824/nimbo · https://nimbo-hc.web.app

## LinkedIn

I'm sharing Nimbo, an open-source desktop app for designing and testing cloud infrastructure locally.

A pattern I keep seeing: validating an architecture idea, or teaching someone how a set of cloud services fits together, takes much more setup than it should — Docker containers, emulators, provider configuration, Terraform state — and every shortcut tends to end up on a real account. Nimbo packages that setup into one tool:

- Visual design: drag resources (APIs, functions, queues, databases…) onto a canvas and connect them; common integrations are wired for you.
- Local execution: each design runs in its own sandbox on your machine with Floci emulators for AWS, Azure, Google Cloud and Oracle Cloud. No cloud account, no cost, nothing leaves your laptop.
- Hands-on testing: call the deployed endpoints, send messages, inspect data, and run load tests to see latency and limits.
- Real Terraform: every diagram is plain Terraform you can read and export, and existing Terraform projects can be imported as diagrams.
- Guided onboarding for newcomers and a Pro mode with full HCL access for experienced engineers; AI agents can also drive it through MCP.

It works well for prototyping before touching a real environment, and for learning or teaching cloud concepts with immediate, visual feedback.

Free and MIT-licensed for macOS, Windows and Linux. Feedback and contributions are welcome.

GitHub: https://github.com/ismaelosuna7824/nimbo
Website: https://nimbo-hc.web.app

#CloudComputing #Terraform #DevOps #PlatformEngineering #OpenSource

## X

### Main post (≤ 280 characters)

Nimbo: an open-source desktop app to design cloud infrastructure visually and run it locally in AWS/Azure/GCP/OCI sandboxes. No account, no cost, real Terraform in and out. Good for prototyping and for learning.

https://github.com/ismaelosuna7824/nimbo

### Optional thread (4 posts)

1/ Validating an architecture idea, or teaching how cloud services fit together, usually means wiring Docker, emulators, provider config and Terraform first. Nimbo packages all of that into one desktop app.

2/ Design on a canvas, then run it in a per-design sandbox on your machine (Floci emulators for AWS, Azure, GCP and OCI). Nothing touches a real account.

3/ Call the endpoints, send messages, inspect data and load-test it. Every diagram is plain Terraform you can read and export, and existing projects import as diagrams.

4/ Guided mode for newcomers, Pro mode with full HCL, MCP for AI agents. Free and MIT, for macOS, Windows and Linux.
https://nimbo-hc.web.app

## Video alt text

Silent product demo of Nimbo, a desktop app, with on-screen captions. A pixel-art cloud mascot introduces "Design, deploy and load-test cloud infrastructure — on your laptop". Cloud resources (API Gateway, Lambda, DynamoDB) are dragged onto a canvas and connected. One click deploys them into a local sandbox of AWS, Azure, Google Cloud and Oracle Cloud emulators in Docker, stamped "$0.00 real cloud bill". A POST /items request returns 201 Created. A live load test charts throughput and latency and marks the breaking point at about 350 requests per second. An AI agent builds and deploys the same API from one prompt through Nimbo's MCP server. A Terraform folder is imported and clean HCL is exported. Closing card: Free · macOS · Windows · Linux, github.com/ismaelosuna7824/nimbo, nimbo-hc.web.app.
