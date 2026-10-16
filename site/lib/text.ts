/**
 * Converts an MDX body into readable Markdown-ish plain text for llms.txt
 * and the search index: JSX tags are removed (their text content stays),
 * import/export lines dropped, code fences kept.
 */
const CALLOUT_LABELS: Record<string, string> = { info: "Note", tip: "Tip", warning: "Warning", danger: "Danger" };

export function mdxToMarkdown(body: string): string {
  const lines: string[] = [];
  let inFence = false;

  for (const line of body.split("\n")) {
    if (/^\s*```/.test(line)) {
      inFence = !inFence;
      lines.push(line.trim().split(/\s+/).slice(0, 1).join(""));
      continue;
    }
    if (inFence) {
      lines.push(line);
      continue;
    }
    if (/^(import|export)\s/.test(line)) continue;

    const titled = line
      .replace(/<Callout\b[^>]*\btype="(\w+)"[^>]*>/g, (_, t: string) => `**${CALLOUT_LABELS[t] ?? "Note"}:** `)
      .replace(/<(Step|Tab|Card)\b[^>]*\btitle="([^"]*)"[^>]*>/g, "**$2** ");
    const stripped = titled.replace(/<\/?[A-Z][A-Za-z]*\b[^>]*\/?>/g, "").replace(/\s+$/, "");
    lines.push(stripped);
  }

  return lines.join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

/** Flattens Markdown into a single line of searchable words. */
export function markdownToPlain(md: string): string {
  return md
    .replace(/```[^\n]*\n([\s\S]*?)```/g, " $1 ")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/^\s*[-*]\s+/gm, "")
    .replace(/\|/g, " ")
    .replace(/[-:]{3,}/g, " ")
    .replace(/(\*\*|__)/g, "")
    .replace(/\s+/g, " ")
    .trim();
}
