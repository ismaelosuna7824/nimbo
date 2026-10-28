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

# Before → after post (release update)

Carousel (4:5, 1080×1350), 10 slides, in English and Spanish. Regenerate with `bun run render:before-after` in
`marketing/` (the app screenshots it uses live in `out/before-after/screens/`).

- LinkedIn document post (recommended, swipeable): attach `out/before-after/nimbo-before-after-es.pdf` to the Spanish
  post or `out/before-after/nimbo-before-after-en.pdf` to the English one. Document title: "Nimbo: antes → después" /
  "Nimbo: before → after".
- Image post instead (LinkedIn allows up to 20 images): `out/before-after/es/*.png` or `out/before-after/en/*.png`, in
  file order: `01-cover`, `02-editor`, `03-toolbar-docker`, `04-inspector`, `05-plan`, `06-live-failure`,
  `07-compliance`, `08-cicd`, `09-navigation-performance`, `10-closing`. Alt text below.

Before = the previous version (commit `06d7baf`); after = this release. Same templates (Serverless REST API deployed
to the local sandbox, Node.js + PostgreSQL + Redis on Docker Compose with monitoring), same 1440×900 window, dark
theme.

## LinkedIn (ES)

Nimbo: antes → después

Nimbo es una app de escritorio open source para diseñar infraestructura cloud de forma visual, desplegarla en tu
máquina (emuladores de AWS, Azure, Google Cloud y Oracle Cloud, Docker Compose o Kubernetes local con k3s), hacerle
pruebas de carga y exportar Terraform, Compose o manifiestos de Kubernetes reales.

Esta versión cambia mucho, así que en el carrusel comparo las mismas pantallas antes y después (mismo template, mismo
sandbox) y después muestro lo nuevo.

La idea de esta versión: menos ruido en pantalla y más información sobre el propio diseño, para saber qué va a cambiar
un plan, cómo se comporta el sistema bajo carga y ante un fallo, y qué marcaría una auditoría, antes de tocar una cuenta
real.

Rediseño y navegación
- Una barra lateral en todas las pantallas: Inicio, Learn, Connections y tus workflows abiertos, como archivos en un
IDE. Puedes tener varios abiertos a la vez.
- Barra de herramientas más limpia: Deploy es un botón dividido (Validate, Plan y ejecuciones; Destroy al final, en
rojo y con confirmación) y las acciones secundarias están en "⋯".
- Colores más tranquilos (el rojo solo aparece cuando algo está roto), tarjetas con un solo punto de estado y un
Inspector con los ajustes agrupados (red, seguridad, capacidad, logs, backup, tags…) en todos los proveedores, Docker y
Kubernetes.

Nuevo
- Plan en el canvas: después de un terraform plan cada recurso que cambia muestra + ~ ± −, y el inspector enseña el diff de
atributos. Nimbo avisa cuando el plan quedó desactualizado.
- Tráfico en vivo durante una prueba de carga (req/s, p95, % de errores) y pruebas de fallo guiadas en Docker y
Kubernetes: detener una dependencia, añadir latencia, matar una réplica o vaciar una caché mientras fluye el tráfico,
con un veredicto al final (resistió, degradado, falló o sin evaluar).
- Cumplimiento: una pre-auditoría del diseño con benchmarks CIS para AWS, Azure, GCP, OCI, Docker y Kubernetes (~300
controles, ~200 automáticos), visible en el canvas y exportable como reporte Markdown para auditores. No es una
certificación.
- Exportar CI/CD: GitHub Actions o GitLab CI con validate y plan en cada PR, apply por entorno con aprobación, build y
push de imágenes, deploy a Kubernetes y smoke check.

Rendimiento
- ~5× menos JavaScript al arrancar, y muchos menos renders y peticiones mientras editas.
- En el daemon, obtener un workflow de 500 nodos pasó de 107 a 19 ms, y listar workflows de 42 a 0.4 ms.

Sigue siendo gratis y con licencia MIT, para macOS, Windows y Linux. Comentarios y contribuciones bienvenidos.

GitHub: https://github.com/ismaelosuna7824/nimbo
Web: https://nimbo-hc.web.app

#CloudComputing #Terraform #DevOps #PlatformEngineering #Kubernetes #OpenSource

## LinkedIn (EN)

Nimbo: before → after

Nimbo is an open-source desktop app to design cloud infrastructure visually, run it on your machine (AWS, Azure,
Google Cloud and Oracle Cloud emulators, Docker Compose, or local Kubernetes with k3s), load-test it, and export real
Terraform, Compose or Kubernetes manifests.

This release changes a lot, so the carousel compares the same screens before and after (same template, same sandbox)
and then walks through what's new.

The aim of this release: less noise on screen and more feedback about the design itself, so you can see what a plan
will change, how the system behaves under load and under failure, and what an audit would flag, before touching a real
account.

Redesign and navigation
- A left rail on every screen: Home, Learn, Connections and your open workflows, like files in an IDE. Several
workflows can be open at once.
- A cleaner toolbar: Deploy is a split button (Validate, Plan and runs; Destroy last, in red, confirmed), and secondary
actions live in "⋯".
- Calmer colors (red only when something is broken), cards with a single status dot, and an Inspector that groups
settings (networking, security, capacity, logging, backup, tags…) for every provider, Docker and Kubernetes.

New
- Plan on the canvas: after a terraform plan every resource that changes shows + ~ ± −, and the inspector shows the attribute diff.
Nimbo tells you when the plan is stale.
- Live traffic during a load test (req/s, p95, error %) and guided failure tests for Docker and Kubernetes: stop a
dependency, add latency, kill a replica or flush a cache while traffic flows, with a verdict at the end (survived,
degraded, failed or not assessed).
- Compliance: a pre-audit of the design against CIS benchmarks for AWS, Azure, GCP, OCI, Docker and Kubernetes (~300
controls, ~200 automatic), shown on the canvas and exportable as a Markdown report for auditors. It is not a
certification.
- CI/CD export: GitHub Actions or GitLab CI with validate and plan on every PR, apply per environment with approval,
image build and push, Kubernetes deploy and a smoke check.

Performance
- ~5× smaller startup JavaScript, and far fewer renders and requests while you edit.
- In the daemon, getting a 500-node workflow went from 107 to 19 ms, and listing workflows from 42 to 0.4 ms.

Still free and MIT-licensed, for macOS, Windows and Linux. Feedback and contributions are welcome.

GitHub: https://github.com/ismaelosuna7824/nimbo
Website: https://nimbo-hc.web.app

#CloudComputing #Terraform #DevOps #PlatformEngineering #Kubernetes #OpenSource

## Carousel alt text (EN)

1. `01-cover.png`: Title "Nimbo: before → after" with the Nimbo cloud mascot, the line "Same workflows. A redesigned
   editor, new navigation, and new tools to plan, break, audit and ship what you design", and a tilted screenshot of
   the new editor in its macOS window.
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
10. `10-closing.png`: "Try Nimbo" with the mascot, "Free · macOS · Windows · Linux", github.com/ismaelosuna7824/nimbo
    and nimbo-hc.web.app, "Open source · MIT".

## Carousel alt text (ES)

1. `01-cover.png`: Título "Nimbo: antes → después" con la mascota de Nimbo (una nube), la frase "Los mismos
   workflows. Un editor rediseñado, navegación nueva y herramientas nuevas para planear, romper, auditar y publicar lo
   que diseñas" y una captura inclinada del nuevo editor en su ventana de macOS.
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
10. `10-closing.png`: "Prueba Nimbo" con la mascota, "Gratis · macOS · Windows · Linux",
    github.com/ismaelosuna7824/nimbo y nimbo-hc.web.app, "Código abierto · MIT".
