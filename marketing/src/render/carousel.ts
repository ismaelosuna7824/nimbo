/**
 * Copy of the before → after LinkedIn carousel (src/pages/before-after/[lang].astro), in English and Spanish.
 * Strings may hold inline HTML (<b>, <code>, <em>). Screenshots live in out/before-after/screens and are
 * served under /shots/ by scripts/render-before-after.ts.
 */
export type Lang = "en" | "es";
export const LANGS: readonly Lang[] = ["en", "es"];

export interface CarouselCopy {
  before: string;
  after: string;
  cover: { kicker: string; sub: string };
  editor: { kicker: string; title: string; note: string };
  toolbar: { kicker: string; title: string; toolbarNote: string; dockerNote: string };
  inspector: { kicker: string; title: string; sub: string };
  plan: { kicker: string; title: string; sub: string };
  live: { kicker: string; title: string; items: string[] };
  compliance: { kicker: string; title: string; sub: string };
  cicd: { kicker: string; title: string; items: string[] };
  nav: { kicker: string; title: string; sub: string; stats: { value: string; label: string }[]; note: string };
  closing: { title: string; badges: string; foot: string };
  newTag: string;
}

export const COPY: Record<Lang, CarouselCopy> = {
  en: {
    before: "Before",
    after: "After",
    newTag: "New",
    cover: {
      kicker: "What changed",
      sub: "Same workflows. A redesigned editor, new navigation, and new tools to plan, break, audit and ship what you design.",
    },
    editor: {
      kicker: "Before / after · Terraform editor",
      title: "Same serverless API, same sandbox",
      note: "<b>After:</b> a left rail on every screen, calmer cards with one status dot, animated edges on deployed links, and Guided mode opens with the bottom panel collapsed.",
    },
    toolbar: {
      kicker: "Before / after · Toolbar and Docker",
      title: "Fewer buttons, same power",
      toolbarNote: "<b>Deploy is a split button:</b> Validate, Plan and runs inside; Destroy last, in red, confirmed. Secondary actions moved to ⋯.",
      dockerNote: "Same Docker Compose stack (API, PostgreSQL, Redis + monitoring), running 6/6 in both.",
    },
    inspector: {
      kicker: "Before / after · Inspector",
      title: "Settings grouped, not listed",
      sub: "Same <code>aws_db_instance</code>. Settings are grouped (General, Security, Storage, Backup, Networking, Logging, Tags…) with a Configured/All switch and a sticky section nav. For every provider, Docker and Kubernetes.",
    },
    plan: {
      kicker: "Plan on the canvas",
      title: "<code>terraform plan</code>, drawn on the diagram",
      sub: "After a plan each changed resource gets a marker (+ create, ~ update, ± replace, − destroy), the inspector shows the attribute diff (here <code>timeout 10 → 30</code>), and Nimbo tells you when the plan is stale.",
    },
    live: {
      kicker: "Live traffic and failure tests",
      title: "Break it on purpose, watch it live",
      items: [
        "<b>Live traffic:</b> during a load test the target shows req/s, p95 and error %; with Prometheus monitoring every service gets its own traffic and animated edges.",
        "<b>Failure tests</b> from the canvas (Docker, Kubernetes): stop a dependency, add latency, kill a replica or flush a cache while traffic flows.",
        "<b>A verdict</b> when it ends: survived, degraded, failed or not assessed.",
      ],
    },
    compliance: {
      kicker: "Compliance",
      title: "A CIS pre-audit of your design",
      sub: "CIS benchmarks for AWS, Azure, GCP, OCI, Docker and Kubernetes: ~300 controls, ~200 checked automatically. Violations show on the canvas, manual controls can be attested, and the result exports as a Markdown report for auditors. A pre-audit, not a certification.",
    },
    cicd: {
      kicker: "CI/CD export",
      title: "Export the pipeline with the code",
      items: [
        "<b>GitHub Actions</b> or <b>GitLab CI</b>",
        "Validate and plan on every pull request",
        "Apply per environment, with approval before production",
        "Build and push images, deploy to Kubernetes",
        "Optional smoke check after each deploy",
      ],
    },
    nav: {
      kicker: "Navigation, design, performance",
      title: "Several workflows open, one calmer app",
      sub: "A left rail on every screen: Home, Learn, Connections and your open workflows, like files in an IDE. On macOS, a custom title bar with a command bar (⌘K).",
      stats: [
        { value: "~5×", label: "smaller startup JavaScript" },
        { value: "107 → 19<small> ms</small>", label: "daemon: get a workflow with 500 nodes" },
        { value: "42 → 0.4<small> ms</small>", label: "daemon: list workflows" },
      ],
      note: "Plus far fewer renders and API requests while you edit.",
    },
    closing: {
      title: "Try <span class=\"cs-accent\">Nimbo</span>",
      badges: "Free · macOS · Windows · Linux",
      foot: "Open source · MIT · feedback and contributions welcome",
    },
  },
  es: {
    before: "Antes",
    after: "Después",
    newTag: "Nuevo",
    cover: {
      kicker: "Qué cambió",
      sub: "Los mismos workflows. Un editor rediseñado, navegación nueva y herramientas nuevas para planear, romper, auditar y publicar lo que diseñas.",
    },
    editor: {
      kicker: "Antes / después · Editor de Terraform",
      title: "La misma API serverless, el mismo sandbox",
      note: "<b>Después:</b> barra lateral en todas las pantallas, tarjetas más tranquilas con un solo punto de estado, conexiones animadas entre recursos desplegados y, en modo Guiado, el panel inferior empieza plegado.",
    },
    toolbar: {
      kicker: "Antes / después · Barra y Docker",
      title: "Menos botones, el mismo poder",
      toolbarNote: "<b>Deploy es un botón dividido:</b> Validate, Plan y ejecuciones dentro; Destroy al final, en rojo y con confirmación. Las acciones secundarias pasaron a ⋯.",
      dockerNote: "El mismo stack de Docker Compose (API, PostgreSQL, Redis + monitoreo), corriendo 6/6 en ambos.",
    },
    inspector: {
      kicker: "Antes / después · Inspector",
      title: "Ajustes agrupados, no listados",
      sub: "El mismo <code>aws_db_instance</code>. Los ajustes se agrupan (General, Seguridad, Almacenamiento, Backup, Red, Logs, Tags…) con un selector Configurados/Todos y navegación fija por secciones. En todos los proveedores, Docker y Kubernetes.",
    },
    plan: {
      kicker: "Plan en el canvas",
      title: "<code>terraform plan</code>, dibujado en el diagrama",
      sub: "Tras un plan cada recurso que cambia lleva un marcador (+ crear, ~ actualizar, ± reemplazar, − destruir), el inspector muestra el diff de atributos (aquí <code>timeout 10 → 30</code>) y Nimbo avisa cuando el plan quedó desactualizado.",
    },
    live: {
      kicker: "Tráfico en vivo y pruebas de fallo",
      title: "Rómpelo a propósito y míralo en vivo",
      items: [
        "<b>Tráfico en vivo:</b> durante una prueba de carga el objetivo muestra req/s, p95 y % de errores; con monitoreo Prometheus cada servicio muestra su propio tráfico y conexiones animadas.",
        "<b>Pruebas de fallo</b> desde el canvas (Docker, Kubernetes): detener una dependencia, añadir latencia, matar una réplica o vaciar una caché mientras fluye el tráfico.",
        "<b>Un veredicto</b> al terminar: resistió, degradado, falló o sin evaluar.",
      ],
    },
    compliance: {
      kicker: "Cumplimiento",
      title: "Una pre-auditoría CIS de tu diseño",
      sub: "Benchmarks CIS para AWS, Azure, GCP, OCI, Docker y Kubernetes: ~300 controles, ~200 verificados automáticamente. Los incumplimientos se ven en el canvas, los controles manuales se pueden atestiguar y el resultado se exporta como reporte Markdown para auditores. Es una pre-auditoría, no una certificación.",
    },
    cicd: {
      kicker: "Exportar CI/CD",
      title: "Exporta el pipeline junto con el código",
      items: [
        "<b>GitHub Actions</b> o <b>GitLab CI</b>",
        "Validate y plan en cada pull request",
        "Apply por entorno, con aprobación antes de producción",
        "Build y push de imágenes, deploy a Kubernetes",
        "Smoke check opcional después de cada deploy",
      ],
    },
    nav: {
      kicker: "Navegación, diseño, rendimiento",
      title: "Varios workflows abiertos, una app más tranquila",
      sub: "Una barra lateral en todas las pantallas: Inicio, Learn, Connections y tus workflows abiertos, como archivos en un IDE. En macOS, barra de título propia con barra de comandos (⌘K).",
      stats: [
        { value: "~5×", label: "menos JavaScript al arrancar" },
        { value: "107 → 19<small> ms</small>", label: "daemon: obtener un workflow de 500 nodos" },
        { value: "42 → 0.4<small> ms</small>", label: "daemon: listar workflows" },
      ],
      note: "Y muchos menos renders y peticiones a la API mientras editas.",
    },
    closing: {
      title: "Prueba <span class=\"cs-accent\">Nimbo</span>",
      badges: "Gratis · macOS · Windows · Linux",
      foot: "Código abierto · MIT · comentarios y contribuciones bienvenidos",
    },
  },
};
