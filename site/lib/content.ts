import fs from "node:fs";
import path from "node:path";
import { cache } from "react";
import matter from "gray-matter";
import { navigation } from "@/content/navigation";
import { extractHeadings, type Heading } from "@/lib/toc";

const CONTENT_DIR = path.join(process.cwd(), "content", "docs");

export type DocMeta = {
  /** URL segments after /docs, e.g. ["basics", "routing"]. */
  slug: string[];
  /** Path under content/docs without extension, e.g. "basics/routing". */
  file: string;
  href: string;
  title: string;
  description: string;
  category: string;
};

export type Doc = DocMeta & {
  body: string;
  headings: Heading[];
};

function readDoc(dir: string, page: string, slug: string[], category: string): Doc {
  const file = `${dir}/${page}`;
  const raw = fs.readFileSync(path.join(CONTENT_DIR, `${file}.mdx`), "utf8");
  const { data, content } = matter(raw);

  if (typeof data.title !== "string" || typeof data.description !== "string") {
    throw new Error(`content/docs/${file}.mdx: front matter needs "title" and "description"`);
  }

  return {
    slug,
    file,
    href: `/docs/${slug.join("/")}`,
    title: data.title,
    description: data.description,
    category,
    body: content,
    headings: extractHeadings(content),
  };
}

/** Every page in navigation order. Throws on missing files or front matter. */
export const getAllDocs = cache((): Doc[] =>
  navigation.flatMap((cat) =>
    cat.pages.map((page) => {
      const prefix = cat.prefix ?? cat.dir;
      const slug = prefix ? [prefix, page] : [page];
      return readDoc(cat.dir, page, slug, cat.title);
    }),
  ),
);

export function getDoc(slug: string[]): Doc | undefined {
  const key = slug.join("/");
  return getAllDocs().find((d) => d.slug.join("/") === key);
}

export function getNeighbours(doc: DocMeta): { prev?: DocMeta; next?: DocMeta } {
  const all = getAllDocs();
  const i = all.findIndex((d) => d.href === doc.href);
  return { prev: all[i - 1], next: all[i + 1] };
}

export type NavGroup = { title: string; items: { title: string; href: string }[] };

export function getNavGroups(): NavGroup[] {
  const all = getAllDocs();
  return navigation.map((cat) => ({
    title: cat.title,
    items: all
      .filter((d) => d.category === cat.title)
      .map((d) => ({ title: d.title, href: d.href })),
  }));
}
