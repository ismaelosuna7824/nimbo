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

# Nimbo v0.13.0 release post

Carousel (4:5, 1080×1350), 10 slides, in English and Spanish. Regenerate with `bun run render:before-after` in
`marketing/` (the app screenshots it uses live in `out/before-after/screens/`).

- LinkedIn document post (recommended, swipeable): attach `out/before-after/nimbo-before-after-es.pdf` to the Spanish
  post or `out/before-after/nimbo-before-after-en.pdf` to the English one. Document title: "Nimbo v0.13.0: lo nuevo" /
  "Nimbo v0.13.0: what's new".
- Image post instead (LinkedIn allows up to 20 images): `out/before-after/es/*.png` or `out/before-after/en/*.png`, in
  file order: `01-cover`, `02-editor`, `03-toolbar-docker`, `04-inspector`, `05-plan`, `06-live-failure`,
  `07-compliance`, `08-cicd`, `09-navigation-performance`, `10-closing`. Alt text below.

The before/after slides compare the previous version (v0.12.1) with this release: same templates (Serverless REST API
deployed to the local sandbox, Node.js + PostgreSQL + Redis on Docker Compose with monitoring), same 1440×900 window,
dark theme.

## LinkedIn (ES)

Nimbo v0.13.0 ya está disponible 🚀

Nimbo es una app de escritorio open source para diseñar infraestructura cloud de forma visual, correrla en tu máquina y
probarla bajo carga antes de tocar una cuenta real. Esta es la versión más grande hasta ahora. Esto es lo nuevo:

🗺️ Plan en el canvas
Después de un terraform plan, cada recurso muestra si se crea (+), cambia (~), se reemplaza (±) o se borra (−), y el
inspector enseña qué atributos cambian. Si editas después, Nimbo avisa que el plan quedó desactualizado.

📈 Tráfico en vivo y pruebas de fallos
Durante una prueba de carga ves en el canvas req/s, p95 y % de errores. En Docker y Kubernetes puedes romper algo a
propósito desde la tarjeta (detener una dependencia, añadir latencia, matar una réplica, vaciar la caché) y ver el
impacto en vivo, con un veredicto al final.

✅ Cumplimiento CIS
Una pre-auditoría del diseño con los benchmarks CIS de AWS, Azure, Google Cloud, Oracle Cloud, Docker y Kubernetes:
unos 300 controles, ~200 revisados automáticamente, resaltados en el canvas y exportables como reporte para auditores.
(No es una certificación: es para llegar a la auditoría con todo corregido.)

⚙️ Exportar CI/CD
Junto con tu Terraform, Compose o manifiestos, Nimbo genera un pipeline de GitHub Actions o GitLab CI: validate y plan
en cada PR, apply por entorno con aprobación para producción, build de imágenes y smoke check.

🎨 App rediseñada
Barra lateral en todas las pantallas, varios workflows abiertos a la vez (como en un IDE), una barra de herramientas
más limpia, un inspector agrupado por categorías y un look más tranquilo donde el rojo solo aparece cuando algo está
roto.

⚡ Más rápida
~5× menos JavaScript al arrancar y un daemon más ágil: abrir un workflow de 500 nodos pasó de 107 a 19 ms.

Y sigue todo lo que ya tenía: diseño visual con ~5.200 recursos de Terraform, sandbox local con emuladores de AWS,
Azure, GCP y OCI, workspaces de Docker Compose y Kubernetes, pruebas de carga, Terraform real de ida y vuelta, rutas y
retos en Learn, y agentes de IA vía MCP.

Gratis y open source (MIT) para macOS, Windows y Linux. Si ya la tienes instalada, se actualiza sola.
En el carrusel: antes y después, y cada función nueva.

Descarga: https://github.com/ismaelosuna7824/nimbo/releases/tag/v0.13.0
Web: https://nimbo-hc.web.app

#CloudComputing #Terraform #DevOps #PlatformEngineering #Kubernetes #OpenSource

## LinkedIn (EN)

Nimbo v0.13.0 is out 🚀

Nimbo is an open-source desktop app to design cloud infrastructure visually, run it on your machine and load-test it
before touching a real account. This is the biggest release so far. Here's what's new:

🗺️ Plan on the canvas
After a terraform plan, every resource shows whether it will be created (+), updated (~), replaced (±) or deleted
(−), and the inspector shows which attributes change. Edit afterwards and Nimbo tells you the plan is stale.

📈 Live traffic and failure tests
During a load test the canvas shows req/s, p95 and error rate. On Docker and Kubernetes you can break something on
purpose from its card (stop a dependency, add latency, kill a replica, flush the cache) and watch the impact live, with
a verdict at the end.

✅ CIS compliance
A pre-audit of the design against the CIS benchmarks for AWS, Azure, Google Cloud, Oracle Cloud, Docker and
Kubernetes: about 300 controls, ~200 checked automatically, highlighted on the canvas and exportable as a report for
auditors. (It isn't a certification: it gets you to the audit with everything fixed.)

⚙️ CI/CD export
Alongside your Terraform, Compose or manifests, Nimbo generates a GitHub Actions or GitLab CI pipeline: validate and
plan on every PR, per-environment apply with approval for production, image builds and a smoke check.

🎨 A redesigned app
A left rail on every screen, several workflows open at once (like an IDE), a cleaner toolbar, an inspector grouped by
category and a calmer look where red only shows up when something is broken.

⚡ Faster
~5× less JavaScript at startup and a snappier daemon: opening a 500-node workflow went from 107 to 19 ms.

Everything it already did is still there: visual design with ~5,200 Terraform resources, a local sandbox with AWS,
Azure, GCP and OCI emulators, Docker Compose and Kubernetes workspaces, load tests, real Terraform in and out, Learn
roadmaps and challenges, and AI agents over MCP.

Free and open source (MIT) for macOS, Windows and Linux. Already installed? It updates itself.
The carousel shows before and after, and each new feature.

Download: https://github.com/ismaelosuna7824/nimbo/releases/tag/v0.13.0
Website: https://nimbo-hc.web.app

#CloudComputing #Terraform #DevOps #PlatformEngineering #Kubernetes #OpenSource

## Carousel alt text (EN)

1. `01-cover.png`: Title "Nimbo v0.13.0" under the label "New release", with the Nimbo cloud mascot, the line "What's
   new: a redesigned app, the plan on the canvas, live traffic and failure tests, CIS compliance and CI/CD export", and
   a tilted screenshot of the new editor in its macOS window.
2. `02-editor.png`: The same Serverless REST API workflow deployed to the local sandbox, before (top) and after
   (bottom). After adds a left rail, calmer resource cards with one status dot and a collapsed bottom panel.
3. `03-toolbar-docker.png`: The editor toolbar before (many icon buttons) and after (Guided/Pro, sandbox status, Test,
   a Deploy split button and a "⋯" menu), the Deploy menu open with Validate, Plan, Runs and logs and a red "Destroy
   everything in the sandbox", and the same Docker Compose canvas (API, PostgreSQL, Redis, monitoring) before and after.
4. `04-inspector.png`: The inspector of the same aws_db_instance side by side: before, one long list of fields; after,
   fields grouped into Required, General and Security & encryption with a Configured/All switch.
5. `05-plan.png`: After a Terraform plan, the canvas marks the new RDS database with + and the Lambda function with ~,
   a "Plan +1 ~1" chip sits on the canvas, and the inspector lists the planned change "timeout 10 → 30".
6. `06-live-failure.png`: A Docker canvas during a load test: the api card shows "50 req/s · p95 3.1ms" and the cache
   card "Cache flushed · watching". Below, the "Test a failure on cache" dialog (kill a replica, dependency down, slow
   dependency, flush the cache) and the result: "Passed", cache flush survived.
7. `07-compliance.png`: The Compliance tab running the CIS AWS Foundations Benchmark: "4 / 5 automatic controls met",
   one violated control (Multi-AZ for RDS) highlighted in red on the canvas, with Show on canvas and Export report
   buttons.
8. `08-cicd.png`: The Export Terraform dialog with the CI/CD pipeline section set to GitHub Actions and a smoke check,
   next to a list: GitHub Actions or GitLab CI, validate and plan on every pull request, apply per environment with
   approval, build and push images and deploy to Kubernetes, optional smoke check.
9. `09-navigation-performance.png`: The Home screen before and after (after adds the left rail), the macOS title bar
   with the workspace name and ⌘K, and three numbers: ~5× smaller startup JavaScript, 107 → 19 ms to get a 500-node
   workflow, 42 → 0.4 ms to list workflows.
10. `10-closing.png`: "Try Nimbo v0.13.0" with the mascot, a line listing what Nimbo does (visual design, local deploys
    on AWS, Azure, GCP, OCI, Docker and Kubernetes, load tests, Terraform in and out, Learn, AI agents via MCP), "Free ·
    macOS · Windows · Linux", github.com/ismaelosuna7824/nimbo and nimbo-hc.web.app, "Open source · MIT".

## Carousel alt text (ES)

1. `01-cover.png`: Título "Nimbo v0.13.0" bajo la etiqueta "Nueva versión", con la mascota de Nimbo (una nube), la
   frase "Lo nuevo: la app rediseñada, el plan en el canvas, tráfico en vivo y pruebas de fallos, cumplimiento CIS y
   exportación a CI/CD" y una captura inclinada del nuevo editor en su ventana de macOS.
2. `02-editor.png`: El mismo workflow Serverless REST API desplegado en el sandbox local, antes (arriba) y después
   (abajo). Después añade una barra lateral, tarjetas más tranquilas con un solo punto de estado y el panel inferior
   plegado.
3. `03-toolbar-docker.png`: La barra de herramientas antes (muchos botones de iconos) y después (Guided/Pro, estado del
   sandbox, Test, Deploy como botón dividido y un menú "⋯"), el menú de Deploy abierto con Validate, Plan, Runs and logs
   y "Destroy everything in the sandbox" en rojo, y el mismo canvas de Docker Compose (API, PostgreSQL, Redis,
   monitoreo) antes y después.
4. `04-inspector.png`: El inspector del mismo aws_db_instance lado a lado: antes, una lista larga de campos; después,
   campos agrupados en Required, General y Security & encryption con un selector Configured/All.
5. `05-plan.png`: Tras un plan de Terraform, el canvas marca la nueva base RDS con + y la función Lambda con ~, un chip
   "Plan +1 ~1" sobre el canvas, y el inspector muestra el cambio planeado "timeout 10 → 30".
6. `06-live-failure.png`: Un canvas de Docker durante una prueba de carga: la tarjeta api muestra "50 req/s · p95
   3.1ms" y la de cache "Cache flushed · watching". Debajo, el diálogo "Test a failure on cache" (matar una réplica,
   dependencia caída, dependencia lenta, vaciar la caché) y el resultado: "Passed", la caché resistió el vaciado.
7. `07-compliance.png`: La pestaña Compliance con el CIS AWS Foundations Benchmark: "4 / 5 automatic controls met", un
   control incumplido (Multi-AZ en RDS) resaltado en rojo en el canvas, con los botones Show on canvas y Export report.
8. `08-cicd.png`: El diálogo Export Terraform con la sección CI/CD pipeline en GitHub Actions y smoke check, junto a una
   lista: GitHub Actions o GitLab CI, validate y plan en cada pull request, apply por entorno con aprobación, build y
   push de imágenes y deploy a Kubernetes, smoke check opcional.
9. `09-navigation-performance.png`: La pantalla de inicio antes y después (después añade la barra lateral), la barra
   de título de macOS con el nombre del workspace y ⌘K, y tres cifras: ~5× menos JavaScript al arrancar, 107 → 19 ms
   para obtener un workflow de 500 nodos, 42 → 0.4 ms para listar workflows.
10. `10-closing.png`: "Prueba Nimbo v0.13.0" con la mascota, una línea con lo que hace Nimbo (diseño visual, despliegue
    local en AWS, Azure, GCP, OCI, Docker y Kubernetes, pruebas de carga, Terraform de ida y vuelta, Learn, agentes de
    IA por MCP), "Gratis · macOS · Windows · Linux", github.com/ismaelosuna7824/nimbo y nimbo-hc.web.app, "Código
    abierto · MIT".
