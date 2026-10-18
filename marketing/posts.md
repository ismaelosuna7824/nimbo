# Nimbo launch posts

Video: `out/nimbo-promo-1080x1350.mp4` for LinkedIn (4:5), `out/nimbo-promo-1920x1080.mp4` for X (16:9),
`out/nimbo-promo-1080x1080.mp4` where a square fits better. Captions are burned in; the video is silent.
Regenerate with `bun run render` in `marketing/`.

Links: https://github.com/ismaelosuna7824/nimbo · https://nimbo-hc.web.app

## LinkedIn

I've been working on a small side project called Nimbo, and I'd like to share it in case it's useful to someone.

When I was learning cloud, the hardest part wasn't the concepts, it was everything around them: setting up Docker containers I didn't fully understand, writing Terraform before I knew what each resource did, and worrying about running something by mistake on a real account.

Nimbo is a desktop app that tries to make that easier:

- You design infrastructure visually: drag resources (an API, a function, a queue, a database…) onto a canvas and connect them.
- It runs everything locally, in a private sandbox on your machine, using Floci emulators for AWS, Azure, Google Cloud and Oracle Cloud. No cloud account and no bill.
- It handles the Docker and Terraform setup for you, and a first-run guide checks what you need step by step.
- You can see what you built working: call your API, send a message to a queue, look at a table, and run small load tests.
- When you're ready, it shows you the Terraform behind your diagram, so you can learn how each piece is written. It also works the other way: import a Terraform folder and see it as a diagram.

It's meant for people who are learning or who just want to try an idea quickly before touching a real cloud. It's free and open source (MIT) for macOS, Windows and Linux.

If you try it, I'd really appreciate your feedback — what's confusing, what's missing, what would help you learn.

GitHub: https://github.com/ismaelosuna7824/nimbo
Website: https://nimbo-hc.web.app

#CloudComputing #LearningInPublic #Terraform #DevOps #OpenSource

## X

### Main post (≤ 280 characters)

I made Nimbo, a free desktop app to practice cloud locally: design infrastructure visually, run it in a local AWS/Azure/GCP/OCI sandbox (no account, no bill) and see the Terraform behind it as you learn.

https://github.com/ismaelosuna7824/nimbo

### Optional thread (4 posts)

1/ Learning cloud often starts with setting up Docker, writing Terraform you don't understand yet, and hoping nothing runs on a real account. I wanted something gentler, so I built Nimbo.

2/ You drag resources onto a canvas and connect them. Nimbo runs them in a private sandbox on your machine (Floci emulators in Docker) and takes care of the setup for you.

3/ Then you can try what you built: call the API, send a message to a queue, check a table, run a small load test. And you can open the Terraform behind the diagram to learn how it's written.

4/ It's free and open source (MIT), for macOS, Windows and Linux. Feedback from people who are learning is very welcome.
https://nimbo-hc.web.app

## Video alt text

Silent product demo of Nimbo, a desktop app, with on-screen captions. A pixel-art cloud mascot introduces "Design, deploy and load-test cloud infrastructure — on your laptop". Cloud resources (API Gateway, Lambda, DynamoDB) are dragged onto a canvas and connected. One click deploys them into a local sandbox of AWS, Azure, Google Cloud and Oracle Cloud emulators in Docker, stamped "$0.00 real cloud bill". A POST /items request returns 201 Created. A live load test charts throughput and latency and marks the breaking point at about 350 requests per second. An AI agent builds and deploys the same API from one prompt through Nimbo's MCP server. A Terraform folder is imported and clean HCL is exported. Closing card: Free · macOS · Windows · Linux, github.com/ismaelosuna7824/nimbo, nimbo-hc.web.app.
