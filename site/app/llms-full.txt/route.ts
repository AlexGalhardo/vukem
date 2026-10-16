import { getAllDocs } from "@/lib/content";
import { mdxToMarkdown } from "@/lib/text";
import { site } from "@/lib/site";

export const dynamic = "force-static";

export function GET() {
  const parts = [`# ${site.name} documentation`, "", `> ${site.tagline}`, ""];

  for (const doc of getAllDocs()) {
    parts.push("---", "", `# ${doc.title}`, "", `Source: ${site.url}${doc.href}`, "", `> ${doc.description}`, "", mdxToMarkdown(doc.body), "");
  }

  return new Response(parts.join("\n"), { headers: { "Content-Type": "text/plain; charset=utf-8" } });
}
