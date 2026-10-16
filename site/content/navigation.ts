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
    pages: ["introduction", "installation", "first-app", "configuration", "directory-structure"],
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
    title: "REST APIs",
    dir: "rest-apis",
    pages: ["overview"],
  },
  {
    title: "Database",
    dir: "database",
    pages: ["overview"],
  },
  {
    title: "Security",
    dir: "security",
    pages: ["overview", "authentication", "social-login"],
  },
  {
    title: "Frontend",
    dir: "frontend",
    pages: ["htmx", "inertia"],
  },
  {
    title: "Testing",
    dir: "testing",
    pages: ["overview"],
  },
  {
    title: "Deployment",
    dir: "deployment",
    pages: ["single-binary"],
  },
];
