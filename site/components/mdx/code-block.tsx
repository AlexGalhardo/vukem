import { highlight, normalizeLang } from "@/lib/highlight";
import { CopyButton } from "@/components/copy-button";

export type CodeBlockProps = { code: string; lang?: string; title?: string };

const LABELS: Record<string, string> = {
  go: "Go",
  bash: "Terminal",
  dotenv: ".env",
  json: "JSON",
  jsonc: "JSON",
  yaml: "YAML",
  html: "HTML",
  typescript: "TypeScript",
  tsx: "TSX",
  sql: "SQL",
  dockerfile: "Dockerfile",
  http: "HTTP",
  text: "Text",
};

export function langLabel(lang?: string): string {
  return LABELS[normalizeLang(lang)] ?? lang ?? "Text";
}

/** Highlighted <pre> with both themes; no chrome. */
export async function Highlighted({ code, lang }: { code: string; lang?: string }) {
  const html = await highlight(code.replace(/\n$/, ""), lang);
  return <div className="code-body" dangerouslySetInnerHTML={{ __html: html }} />;
}

/** Fenced code block: filename (or language) header, copy button, code. */
export function CodeBlock({ code, lang, title }: CodeBlockProps) {
  return (
    <figure className="overflow-hidden rounded-xl border border-border bg-code-bg">
      <figcaption className="flex h-10 items-center justify-between border-b border-border pr-1.5 pl-4">
        <span className="truncate font-mono text-xs text-muted">{title ?? langLabel(lang)}</span>
        <CopyButton text={code.replace(/\n$/, "")} />
      </figcaption>
      <Highlighted code={code} lang={lang} />
    </figure>
  );
}
