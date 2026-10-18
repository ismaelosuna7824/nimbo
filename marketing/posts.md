# Nimbo launch posts

Video: `out/nimbo-promo-1080x1350.mp4` for LinkedIn (4:5), `out/nimbo-promo-1920x1080.mp4` for X (16:9),
`out/nimbo-promo-1080x1080.mp4` where a square fits better. Captions are burned in; the video is silent.
Regenerate with `bun run render` in `marketing/`.

Links: https://github.com/ismaelosuna7824/nimbo · https://nimbo-hc.web.app

## LinkedIn

I built Nimbo: design, deploy and load-test cloud infrastructure on your laptop, without touching a real cloud account.

Testing a cloud architecture usually means a real account, a bill, and a slow loop between diagrams, Terraform and dashboards. Nimbo puts that whole loop on your machine:

- Design on a canvas: drag in resources from the AWS, Azure, Google Cloud and Oracle Cloud Terraform providers and connect them.
- Deploy with one click into a private local cloud: Floci emulators running in Docker, one sandbox per workflow. $0 cloud bill.
- Use what you deployed: the Endpoints tab gives you URLs, ready snippets and a built-in "Try it" (POST /items → 201).
- Load-test it live: smoke, load, stress, spike and soak runs with latency percentiles, error rates and the breaking point.
- Terraform both ways: export clean, `terraform fmt`-formatted files, or import an existing Terraform folder and get the diagram back.

What makes it different: it runs locally, it's free and open source (MIT), and it isn't a closed box. Terraform goes in and out, and AI agents (Claude Code, Cursor, Codex and others) can design, deploy and test through its built-in MCP server.

It's a desktop app for macOS, Windows and Linux. Feedback and issues are very welcome.

⭐ GitHub: https://github.com/ismaelosuna7824/nimbo
🌐 Website: https://nimbo-hc.web.app

#Terraform #CloudComputing #DevOps #OpenSource #PlatformEngineering

## X

### Main post (≤ 280 characters)

Design, deploy and load-test cloud infrastructure on your laptop.

Nimbo: drag & connect AWS/Azure/GCP/OCI resources, deploy to a local cloud in Docker ($0 bill), load-test it, export Terraform. Free and open source.

https://github.com/ismaelosuna7824/nimbo

### Optional thread (4 posts)

1/ Nimbo is a desktop app (macOS, Windows, Linux) for designing cloud infrastructure on a canvas: resources from the AWS, Azure, Google Cloud and Oracle Cloud Terraform providers, connected by dragging.

2/ One click deploys it into a private local cloud: Floci emulators in Docker, one sandbox per workflow. No cloud account, no bill. Then call your API from the Endpoints tab (POST /items → 201).

3/ Load-test what you deployed (smoke, load, stress, spike, soak) with live latency, errors and the breaking point. Terraform both ways: export fmt-clean files or import an existing folder.

4/ AI agents can do all of it too, through Nimbo's built-in MCP server (Claude Code, Cursor, Codex…). MIT licensed.
https://nimbo-hc.web.app

## Video alt text

Silent product demo of Nimbo, a desktop app, with on-screen captions. A pixel-art cloud mascot introduces "Design, deploy and load-test cloud infrastructure — on your laptop". Cloud resources (API Gateway, Lambda, DynamoDB) are dragged onto a canvas and connected. One click deploys them into a local sandbox of AWS, Azure, Google Cloud and Oracle Cloud emulators in Docker, stamped "$0.00 real cloud bill". A POST /items request returns 201 Created. A live load test charts throughput and latency and marks the breaking point at about 350 requests per second. An AI agent builds and deploys the same API from one prompt through Nimbo's MCP server. A Terraform folder is imported and clean HCL is exported. Closing card: Free · macOS · Windows · Linux, github.com/ismaelosuna7824/nimbo, nimbo-hc.web.app.
