import { createHighlighter, type Highlighter } from "shiki";

const LANGS = [
  "go",
  "bash",
  "shellscript",
  "json",
  "jsonc",
  "dotenv",
  "yaml",
  "html",
  "typescript",
  "tsx",
  "sql",
  "dockerfile",
  "http",
  "ini",
  "toml",
] as const;

const ALIASES: Record<string, string> = {
  sh: "bash",
  shell: "bash",
  console: "bash",
  env: "dotenv",
  ts: "typescript",
  yml: "yaml",
  gohtml: "html",
  tmpl: "html",
};

let highlighter: Promise<Highlighter> | undefined;

function getHighlighter(): Promise<Highlighter> {
  highlighter ??= createHighlighter({
    themes: ["github-light", "github-dark"],
    langs: [...LANGS],
  });
  return highlighter;
}

export function normalizeLang(lang?: string | null): string {
  if (!lang) return "text";
  const l = lang.toLowerCase();
  const resolved = ALIASES[l] ?? l;
  return (LANGS as readonly string[]).includes(resolved) ? resolved : "text";
}

/**
 * Highlights code with both themes as CSS variables (--shiki-light,
 * --shiki-dark) so switching the theme needs no re-render.
 */
export async function highlight(code: string, lang?: string | null): Promise<string> {
  const h = await getHighlighter();
  return h.codeToHtml(code, {
    lang: normalizeLang(lang),
    themes: { light: "github-light", dark: "github-dark" },
    defaultColor: false,
  });
}
