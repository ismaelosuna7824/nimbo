import type { Strings } from "./en";

export const es: Strings = {
  meta: {
    title: "Nimbo — Diseña, despliega y prueba bajo carga infraestructura cloud en tu laptop",
    description:
      "Nimbo es una app de escritorio gratuita para diseñar infraestructura cloud conectando nodos, exportar o importar Terraform, desplegar en una nube local privada (AWS, Azure, GCP, OCI) y hacerle pruebas de carga, sin tocar una cuenta real.",
    ogImageAlt: "La mascota de Nimbo junto al titular: Diseña, despliega y prueba bajo carga infraestructura cloud en tu laptop.",
  },
  a11y: {
    skip: "Saltar al contenido",
    home: "Inicio de Nimbo",
    primaryNav: "Principal",
    footerNav: "Pie de página",
    menu: "Menú",
    copied: "Copiado al portapapeles",
    externalNew: "(abre GitHub)",
  },
  nav: {
    features: "Funciones",
    how: "Cómo funciona",
    agents: "Agentes de IA",
    start: "Empezar",
    faq: "Preguntas",
    github: "GitHub",
    download: "Descargar",
    language: "Idioma",
  },
  os: {
    mac: "macOS",
    windows: "Windows",
    linux: "Linux",
  },
  hero: {
    eyebrow: "Un ADE local para infraestructura cloud",
    titleStart: "Diseña, despliega y prueba bajo carga tu infraestructura cloud",
    titleAccent: "en tu laptop.",
    sub: "Arrastra y conecta recursos de AWS, Azure, Google Cloud y OCI, obtén Terraform limpio, ejecútalo en una nube local privada y mira cómo aguanta el tráfico. Nada toca tu cuenta real ni tu factura.",
    downloadFor: "Descargar para {os}",
    download: "Descargar Nimbo",
    downloadDesktop: "Consíguelo para tu computadora",
    github: "Ver en GitHub",
    meta: "Gratis · Corre en local · macOS, Windows, Linux",
  },
  how: {
    eyebrow: "Cómo funciona",
    title: "De la idea a infraestructura probada en cuatro pasos",
    sub: "Diséñala, ejecútala en local, ponla bajo carga y llévate el Terraform.",
  },
  features: {
    eyebrow: "Funciones",
    title: "Todo lo que hay entre la pizarra y producción",
    sub: "Una sola app para lo que normalmente exige una cuenta real, un montón de archivos de configuración y algo de valentía.",
    items: {
      design: {
        eyebrow: "Diseña conectando",
        title: "Arrastra, suelta, conecta. Esa es tu arquitectura.",
        body: "Cada recurso de los providers de Terraform de AWS, Azure, Google Cloud y OCI —unos 5.200 tipos— es un nodo en el lienzo. Conecta dos nodos y Nimbo hace el cableado por ti.",
        bullets: [
          "Modo Guiado: primero los campos obligatorios, valores por defecto sensatos y recetas de un clic como “SQS → disparador de Lambda”",
          "Modo Pro: todos los atributos, expresiones HCL, meta-argumentos y paleta de comandos ⌘K",
          "Variables por entorno, con los secretos enmascarados",
        ],
      },
      terraform: {
        eyebrow: "Terraform en ambos sentidos",
        title: "Exporta Terraform limpio. O trae el tuyo.",
        body: "Exporta cualquier workflow como archivos idiomáticos, limpios según terraform fmt, con un .tfvars por entorno. ¿Ya tienes Terraform? Importa una carpeta entera —módulos locales incluidos— y recupera el diagrama, con las conexiones derivadas de las referencias.",
        bullets: [
          "HCL plano y tuyo: sin lock-in ni formatos propietarios",
          "Un archivo .tfvars por entorno",
          "Importa la carpeta de un repo y, por fin, ve su arquitectura",
        ],
      },
      sandbox: {
        eyebrow: "Nube local privada",
        title: "Despliega en una nube que vive en tu laptop.",
        body: "Cada workflow tiene su propio sandbox en Docker con los emuladores Floci que necesita para AWS, Azure, Google Cloud y OCI. Terraform corre dentro y el lienzo muestra qué está desplegado.",
        bullets: [
          "Nada toca tu cuenta cloud real ni tu factura",
          "Un sandbox aislado por workflow",
          "Las URLs locales y credenciales de prueba se mantienen entre reinicios",
        ],
      },
      endpoints: {
        eyebrow: "Endpoints y Try it",
        title: "Cada API, cola, bucket y tabla, lista para usar.",
        body: "La pestaña Endpoints explica cada recurso desplegado en lenguaje claro, con URLs e identificadores copiables y snippets que puedes pegar al instante.",
        bullets: [
          "Snippets para curl, AWS CLI, JavaScript y Python",
          "Try it: envía peticiones y mensajes y vuelve a leerlos",
          "Exporta una colección de Postman con un clic",
        ],
      },
      loadtest: {
        eyebrow: "Pruebas de carga",
        title: "Descubre cómo se comporta con tráfico antes que producción.",
        body: "Elige smoke, load, stress, spike o soak, o diseña tus propias etapas. Mira en vivo los percentiles de latencia, los errores y un puntaje de estabilidad, encuentra el punto de quiebre y define umbrales de aprobado/fallido.",
        bullets: [
          "Latencia p50 / p95 / p99, tasa de error y puntaje de estabilidad en vivo",
          "Endpoints HTTP y operaciones de AWS: SQS, S3, DynamoDB, Lambda, SNS, Kinesis, EventBridge, Step Functions",
          "Historial de ejecuciones y comparación lado a lado",
        ],
      },
      mcp: {
        eyebrow: "Agentes de IA vía MCP",
        title: "Deja que tu agente de IA lo construya contigo.",
        body: "Nimbo incluye un servidor MCP. Tu agente puede crear y ampliar workflows, desplegarlos en el sandbox, llamar a lo que desplegó y hacerle pruebas de carga, y tú ves cada cambio en vivo en el lienzo.",
        agentsLabel: "Instalación con un clic desde Ajustes para",
        promptsLabel: "Prueba a pedirle",
        prompts: [
          "Crea una API HTTP con una Lambda y una tabla DynamoDB y hazle una prueba de carga.",
          "Importa el Terraform de ~/code/platform y explícame qué contiene.",
          "Despliega este workflow y haz una prueba de carga a la API: necesito p95 menor a 200 ms con 50 rps.",
        ],
      },
      connections: {
        eyebrow: "Conexiones",
        title: "Prueba los servicios que ya tienes.",
        body: "Guarda Conexiones a lo que ya corre en otro lado —cualquier API HTTP, PostgreSQL, MySQL, Redis, MongoDB o una cuenta real de AWS mediante un perfil— y hazles pruebas de carga desde la pantalla de inicio o desde cualquier workflow.",
        bullets: [
          "Autenticación por cabeceras, bearer, basic y API key",
          "Los secretos se quedan en tu computadora y no se vuelven a mostrar",
          "El tráfico que sale del sandbox local siempre pide confirmación",
        ],
      },
    },
  },
  modes: {
    eyebrow: "Guiado y Pro",
    title: "Hecho para principiantes y para pros",
    sub: "Cambia de modo cuando quieras. Mismo workflow, mismo Terraform; solo cambia el nivel de detalle.",
    guided: {
      title: "Guiado",
      body: "¿Nuevo en la nube? Empieza aquí.",
      bullets: [
        "Solo los campos que necesitas, con valores recomendados",
        "Recetas de conexión de un clic",
        "Explicaciones en lenguaje claro de lo que desplegaste",
        "Una guía inicial que revisa Docker por ti",
      ],
    },
    pro: {
      title: "Pro",
      body: "Sabes Terraform. Ve rápido.",
      bullets: [
        "Todos los atributos y expresiones HCL",
        "Meta-argumentos y edición de código",
        "Paleta de comandos ⌘K",
        "Importa carpetas existentes, exporta código limpio",
      ],
    },
  },
  start: {
    eyebrow: "Empezar",
    title: "Empieza con un solo comando",
    sub: "Un script revisa tu computadora, instala lo que falte (preguntando antes y desde fuentes oficiales) y compila la app.",
    unix: "macOS / Linux",
    windows: "Windows (PowerShell)",
    copy: "Copiar",
    copied: "Copiado",
    or: "¿Prefieres la app lista para usar?",
    download: "Descarga la app",
    docker:
      "No necesitas Docker para compilar. Al abrir Nimbo por primera vez, revisa Docker para tu sistema y las imágenes del sandbox, te explica lo que falte en lenguaje claro y lo arregla con un clic cuando puede.",
    docs: "Guía de instalación",
  },
  faq: {
    eyebrow: "Preguntas frecuentes",
    title: "Preguntas, respondidas",
    items: [
      {
        id: "free",
        q: "¿Es gratis?",
        a: "Sí. Nimbo es gratis y corre en tu propia computadora: sin cuenta, sin registro, sin suscripción. El código fuente está en GitHub.",
      },
      {
        id: "aws-account",
        q: "¿Toca mi cuenta de AWS?",
        a: "No. Los despliegues van a un sandbox privado en tu máquina, impulsado por los emuladores Floci en Docker. Solo se usa una cuenta real si guardas explícitamente una Conexión a ella, y todo tráfico que salga del sandbox local te pide confirmación antes.",
      },
      {
        id: "docker",
        q: "¿Necesito Docker?",
        a: "Sí, para la nube local: sirven Docker Desktop, OrbStack, Colima, Rancher Desktop, Docker Engine en Linux o Podman con la CLI docker. No lo necesitas para compilar la app: la guía inicial revisa Docker para tu sistema y te ayuda a resolver lo que falte. Tampoco necesitas Terraform instalado.",
      },
      {
        id: "clouds",
        q: "¿Qué nubes y servicios soporta?",
        a: "Para diseñar y generar Terraform: todos los recursos de los providers aws, azurerm, google y oci, unos 5.200 tipos. Para desplegar en local: lo que soportan los emuladores Floci para AWS, Azure, Google Cloud y OCI. Nimbo te indica qué recursos pueden correr en local.",
      },
      {
        id: "export",
        q: "¿Puedo exportar mi Terraform?",
        a: "Sí. Cada workflow se exporta como archivos idiomáticos, limpios según terraform fmt, con un .tfvars por entorno. Es Terraform normal que puedes versionar y aplicar donde quieras. También puedes importar una carpeta existente y recuperar el diagrama.",
      },
      {
        id: "agents",
        q: "¿Cómo se conectan los agentes de IA?",
        a: "Con el servidor MCP integrado de Nimbo. Abre Ajustes → Agentes de IA y pulsa Instalar en Claude Code, Claude Desktop, Cursor, Codex, VS Code, Windsurf o Gemini CLI, o registra `nimbo mcp` a mano. Los cambios del agente aparecen en vivo en la app.",
      },
      {
        id: "data",
        q: "¿Se envían mis datos a algún lado?",
        a: "La app es local-first: workflows, Conexiones y secretos se quedan en tu computadora (en ~/.nimbo). Este sitio web usa Google Analytics para Firebase solo si aceptas el aviso de cookies, y puedes cambiar tu elección cuando quieras desde “Configurar cookies” en el pie de página.",
      },
    ],
  },
  cta: {
    title: "Tu próxima arquitectura está a un arrastre de distancia.",
    sub: "Gratis, local y lista cuando tú lo estés.",
  },
  footer: {
    tagline: "Un ADE local para infraestructura cloud.",
    product: "Producto",
    docs: "Documentación",
    docsReadme: "README",
    docsSetup: "Instalación",
    docsMcp: "Servidor MCP",
    docsArchitecture: "Arquitectura",
    releases: "Versiones",
    poweredBy: "Nube local impulsada por",
    poweredByName: "los emuladores Floci",
    cookies: "Configurar cookies",
    license: "Código fuente en GitHub. Terraform es una marca de HashiCorp; AWS, Azure, Google Cloud y OCI son marcas de sus respectivos dueños.",
  },
  consent: {
    title: "Cookies de analítica",
    text: "Nos gustaría usar Google Analytics para Firebase para entender cómo se usa este sitio web. Solo se activa si aceptas. La app de Nimbo no incluye analítica.",
    accept: "Aceptar",
    decline: "Rechazar",
  },
  notFound: {
    title: "Página no encontrada",
    body: "Esta página se fue flotando como una nube. Volvamos al inicio.",
    home: "Volver al inicio",
  },
};
