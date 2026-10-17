import fs from "node:fs";
import path from "node:path";
import { cache } from "react";
import matter from "gray-matter";
import { navigation, type NavCategory } from "@/content/navigation";
import { navigation as navigationV0_20 } from "@/content/versions/0.20/navigation";
import { defaultLocale, localizeHref, type Locale } from "@/lib/i18n";
import { lastUpdated } from "@/lib/last-updated";
import { extractHeadings, type Heading } from "@/lib/toc";
import { currentVersion, docsBase, versions, type DocVersion } from "@/lib/versions";

const CONTENT_ROOT = path.join(process.cwd(), "content");

/** Sidebar of each version in lib/versions.ts. */
const NAVIGATION: Record<string, NavCategory[]> = {
  "1.0": navigation,
  "0.20": navigationV0_20,
};

export type DocMeta = {
  /** URL segments after /docs, e.g. ["basics", "routing"] or ["0.20", "basics", "routing"]. */
  slug: string[];
  /** Path under site/content, e.g. "docs/basics/routing.mdx" or "pt-br/docs/getting-started/introduction.mdx". */
  source: string;
  href: string;
  title: string;
  description: string;
  category: string;
  version: DocVersion;
  locale: Locale;
  /** False when a non-English page falls back to the English text. */
  translated: boolean;
  /** ISO 8601 date of the source file's last commit, if known. */
  updated?: string;
};

export type Doc = DocMeta & {
  body: string;
  headings: Heading[];
};

function readDoc(source: string): { title: string; description: string; body: string } {
  const raw = fs.readFileSync(path.join(CONTENT_ROOT, source), "utf8");
  const { data, content } = matter(raw);
  if (typeof data.title !== "string" || typeof data.description !== "string") {
    throw new Error(`content/${source}: front matter needs "title" and "description"`);
  }
  return { title: data.title, description: data.description, body: content };
}

function makeDoc(v: DocVersion, source: string, slug: string[], category: string): Doc {
  const { title, description, body } = readDoc(source);
  return {
    slug,
    source,
    href: `/docs/${slug.join("/")}`,
    title,
    description,
    category,
    version: v,
    locale: defaultLocale,
    translated: true,
    updated: lastUpdated(source),
    body,
    headings: extractHeadings(body),
  };
}

/**
 * The page in locale: its translation at content/<locale>/<source> when it
 * exists, otherwise the English page with localized URLs and translated=false.
 */
function localize(doc: Doc, locale: Locale): Doc {
  if (locale === defaultLocale) return doc;
  const href = localizeHref(doc.href, locale);
  const source = `${locale}/${doc.source}`;
  if (!fs.existsSync(path.join(CONTENT_ROOT, source))) return { ...doc, href, locale, translated: false };
  const { title, description, body } = readDoc(source);
  return { ...doc, href, locale, source, title, description, body, headings: extractHeadings(body), updated: lastUpdated(source) };
}

function versionById(id: string): DocVersion {
  const v = versions.find((x) => x.id === id);
  if (!v || !NAVIGATION[id]) throw new Error(`unknown docs version ${id}: register it in lib/versions.ts and lib/content.ts`);
  return v;
}

/**
 * Every page of a version (default: current) in navigation order. Other
 * locales cover only the current version. Throws on missing files or front matter.
 */
export const getAllDocs = cache((versionId: string = currentVersion.id, locale: Locale = defaultLocale): Doc[] => {
  const v = versionById(versionId);
  if (locale !== defaultLocale && versionId !== currentVersion.id) return [];
  return NAVIGATION[versionId].flatMap((cat) =>
    cat.pages.map((page) => {
      const prefix = cat.prefix ?? cat.dir;
      const slug = [v.prefix, prefix, page].filter((s) => s !== "");
      return localize(makeDoc(v, `${v.contentDir}/${cat.dir}/${page}.mdx`, slug, cat.title), locale);
    }),
  );
});

/** Every English page of every version. */
export function getEveryDoc(): Doc[] {
  return versions.flatMap((v) => getAllDocs(v.id));
}

export function getDoc(slug: string[], locale: Locale = defaultLocale): Doc | undefined {
  const key = slug.join("/");
  const docs = locale === defaultLocale ? getEveryDoc() : getAllDocs(currentVersion.id, locale);
  return docs.find((d) => d.slug.join("/") === key);
}

export function getNeighbours(doc: DocMeta): { prev?: DocMeta; next?: DocMeta } {
  const all = getAllDocs(doc.version.id, doc.locale);
  const i = all.findIndex((d) => d.href === doc.href);
  return { prev: all[i - 1], next: all[i + 1] };
}

export type NavGroup = { title: string; items: { title: string; href: string }[] };

export function getNavGroups(versionId: string = currentVersion.id, locale: Locale = defaultLocale): NavGroup[] {
  const all = getAllDocs(versionId, locale);
  return NAVIGATION[versionId].map((cat) => ({
    title: cat.title,
    items: all
      .filter((d) => d.category === cat.title)
      .map((d) => ({ title: d.title, href: d.href })),
  }));
}

/** Sidebar groups keyed by version id, for client components that pick by pathname. */
export function getNavGroupsByVersion(locale: Locale = defaultLocale): Record<string, NavGroup[]> {
  const vs = locale === defaultLocale ? versions : [currentVersion];
  return Object.fromEntries(vs.map((v) => [v.id, getNavGroups(v.id, locale)]));
}

/** URL of the first page of a version. */
export function versionHome(v: DocVersion): string {
  return `${docsBase(v)}/introduction`;
}
