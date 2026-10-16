import GithubSlugger from "github-slugger";
import { getAllDocs } from "@/lib/content";
import { markdownToPlain, mdxToMarkdown } from "@/lib/text";
import { plainInline } from "@/lib/toc";

export type SearchEntry = {
  /** Page title. */
  title: string;
  /** Section heading, absent for the page entry. */
  heading?: string;
  href: string;
  category: string;
  text: string;
};

const MAX_TEXT = 700;

/** One entry per page plus one per h2/h3 section. */
export function buildSearchIndex(): SearchEntry[] {
  const entries: SearchEntry[] = [];

  for (const doc of getAllDocs()) {
    const md = mdxToMarkdown(doc.body);
    const slugger = new GithubSlugger();
    let heading: string | undefined;
    let id = "";
    let buf: string[] = [];
    let inFence = false;

    const flush = () => {
      const text = markdownToPlain(buf.join("\n")).slice(0, MAX_TEXT);
      if (heading) {
        entries.push({ title: doc.title, heading, href: `${doc.href}#${id}`, category: doc.category, text });
      } else {
        entries.push({ title: doc.title, href: doc.href, category: doc.category, text: `${doc.description} ${text}`.trim() });
      }
      buf = [];
    };

    for (const line of md.split("\n")) {
      if (/^\s*```/.test(line)) inFence = !inFence;
      const h = !inFence && line.match(/^(#{1,6})\s+(.+)$/);
      if (h) {
        const text = plainInline(h[2]);
        const slug = slugger.slug(text);
        if (h[1].length === 2 || h[1].length === 3) {
          flush();
          heading = text;
          id = slug;
          continue;
        }
      }
      buf.push(line);
    }
    flush();
  }

  return entries;
}
