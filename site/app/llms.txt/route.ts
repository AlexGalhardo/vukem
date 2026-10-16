import { getNavGroups, getAllDocs } from "@/lib/content";
import { site } from "@/lib/site";

export const dynamic = "force-static";

export function GET() {
  const docs = new Map(getAllDocs().map((d) => [d.href, d]));
  const lines = [
    `# ${site.name}`,
    "",
    `> ${site.tagline} Lumek is a web framework built on net/http for REST APIs and full-stack apps. It is in its Prototype phase; APIs will change.`,
    "",
    `Full documentation in one file: ${site.url}/llms-full.txt`,
    "",
  ];

  for (const group of getNavGroups()) {
    lines.push(`## ${group.title}`, "");
    for (const item of group.items) {
      const doc = docs.get(item.href);
      lines.push(`- [${item.title}](${site.url}${item.href}): ${doc?.description ?? ""}`);
    }
    lines.push("");
  }

  return new Response(lines.join("\n"), { headers: { "Content-Type": "text/plain; charset=utf-8" } });
}
