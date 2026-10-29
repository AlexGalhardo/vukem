/**
 * Sidebar navigation. Each category maps to a folder in content/docs and
 * lists its pages in display order. A page "<dir>/<slug>.mdx" is served at
 * /docs/<prefix>/<slug> (prefix defaults to the folder name; the
 * "Getting started" pages live at the root of /docs).
 */
export type NavCategory = {
  title: string;
  dir: string;
  prefix?: string;
  pages: string[];
};

export const navigation: NavCategory[] = [
  {
    title: "Getting started",
    dir: "getting-started",
    prefix: "",
    pages: ["introduction", "prerequisites", "installation", "starters", "first-app", "configuration", "directory-structure", "showcase"],
  },
  {
    title: "Architecture",
    dir: "architecture",
    pages: ["lifecycle"],
  },
  {
    title: "The basics",
    dir: "basics",
    pages: [
      "routing",
      "middleware",
      "controllers",
      "http-context",
      "requests",
      "responses",
      "validation",
      "error-handling",
      "static-files",
      "sessions",
      "csrf",
      "views",
    ],
  },
  {
    title: "APIs",
    dir: "rest-apis",
    pages: [
      "overview",
      "tutorial",
      "pagination",
      "typed-handlers",
      "openapi",
      "client-generation",
      "idempotency",
      "graphql",
      "grpc",
    ],
  },
  {
    title: "Database",
    dir: "database",
    pages: ["overview", "orm", "relations", "migrations", "factories-seeders", "redis", "mongodb"],
  },
  {
    title: "Security",
    dir: "security",
    pages: [
      "overview",
      "authentication",
      "social-login",
      "jwt",
      "api-tokens",
      "two-factor",
      "passkeys",
      "authorization",
      "rate-limiting",
      "bot-challenge",
      "security-headers",
      "signed-urls",
      "encryption",
      "building-blocks",
    ],
  },
  {
    title: "Digging deeper",
    dir: "digging-deeper",
    pages: [
      "mail",
      "cache",
      "storage",
      "uploads",
      "exports",
      "events",
      "queue",
      "scheduler",
      "sse",
      "broadcasting",
      "notifications",
      "webhooks",
      "localization",
      "billing",
      "admin",
      "feature-flags",
      "devtools",
      "opentelemetry",
      "telegram-logging",
    ],
  },
  {
    title: "Frontend",
    dir: "frontend",
    pages: ["htmx", "vite", "inertia", "inertia-i18n"],
  },
  {
    title: "Testing",
    dir: "testing",
    pages: ["overview"],
  },
  {
    title: "CLI",
    dir: "cli",
    pages: [
      "overview",
      "reference",
      "reference-new",
      "reference-make",
      "reference-install",
      "reference-database",
      "reference-runtime",
    ],
  },
  {
    title: "Deployment",
    dir: "deployment",
    pages: ["single-binary", "docker-and-railway", "cloudflare"],
  },
];
