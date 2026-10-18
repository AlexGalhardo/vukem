import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ArrowRight, SquarePen } from "lucide-react";
import { Toc } from "@/components/toc";
import { getAllDocs, getNeighbours, versionHome, type Doc } from "@/lib/content";
import { formatUpdated, t } from "@/lib/ui-text";
import { renderMdx } from "@/lib/mdx";
import { site } from "@/lib/site";
import { currentVersion, docsBase, type DocVersion } from "@/lib/versions";

/** The same page in the current docs, or the current introduction. */
function latestHref(href: string, v: DocVersion): string {
  const page = href.slice(docsBase(v).length);
  const target = `/docs${page}`;
  return getAllDocs(currentVersion.id).some((d) => d.href === target) ? target : versionHome(currentVersion);
}

export function docMetadata(doc: Doc): Metadata {
  const title = doc.version.id === currentVersion.id ? doc.title : `${doc.title} (${doc.version.label})`;
  return {
    title,
    description: doc.description,
    alternates: { canonical: doc.href },
    openGraph: { title: doc.title, description: doc.description, url: doc.href, locale: "en" },
  };
}

export async function DocArticle({ doc }: { doc: Doc }): Promise<React.ReactElement> {
  const outdated = doc.version.id !== currentVersion.id;
  const content = await renderMdx(doc.body, doc.source, doc.version);
  const { prev, next } = getNeighbours(doc);

  return (
    // justify-between pins the TOC to the container's right edge, aligned with the header actions.
    <div className="flex justify-between gap-12">
      <main id="content" className="min-w-0 flex-1 py-10 lg:pl-8 xl:max-w-3xl">
        {outdated ? (
          <div role="note" className="mb-8 rounded-xl border border-border bg-subtle px-4 py-3 text-sm text-muted">
            You are reading the {doc.version.label} documentation.{" "}
            <Link href={latestHref(doc.href, doc.version)} className="font-medium text-accent hover:underline">
              Go to the latest version ({currentVersion.label})
            </Link>
          </div>
        ) : null}
        <header className="mb-10">
          <p className="mb-2 text-sm font-medium text-accent">{doc.category}</p>
          <div>
            <h1 className="text-3xl font-semibold tracking-tight text-fg sm:text-[2.125rem]">{doc.title}</h1>
            <p className="mt-3 text-lg leading-relaxed text-muted">{doc.description}</p>
          </div>
          {doc.updated ? (
            <p className="mt-3 text-sm text-faint">
              {t.lastUpdated}: <time dateTime={doc.updated}>{formatUpdated(doc.updated)}</time>
            </p>
          ) : null}
        </header>

        <article className="prose">
          {content}
        </article>

        <footer className="mt-16 space-y-8">
          <a
            href={`${site.editBase}/${doc.source}`}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 text-sm text-muted transition-colors hover:text-fg"
          >
            <SquarePen aria-hidden className="size-3.5" />
            {t.editOnGitHub}
          </a>

          <nav aria-label={t.pagination} className="grid gap-3 border-t border-border pt-8 sm:grid-cols-2">
            {prev ? (
              <Link href={prev.href} className="group rounded-xl border border-border p-4 transition-colors hover:border-border-strong hover:bg-subtle">
                <span className="flex items-center gap-1 text-xs text-faint">
                  <ArrowLeft aria-hidden className="size-3" /> {t.previous}
                </span>
                <span className="mt-1 block text-sm font-medium text-fg">{prev.title}</span>
              </Link>
            ) : (
              <span className="hidden sm:block" />
            )}
            {next ? (
              <Link
                href={next.href}
                className="group rounded-xl border border-border p-4 text-right transition-colors hover:border-border-strong hover:bg-subtle"
              >
                <span className="flex items-center justify-end gap-1 text-xs text-faint">
                  {t.next} <ArrowRight aria-hidden className="size-3" />
                </span>
                <span className="mt-1 block text-sm font-medium text-fg">{next.title}</span>
              </Link>
            ) : null}
          </nav>
        </footer>
      </main>

      <aside className="sticky top-(--header-h) hidden h-[calc(100dvh-var(--header-h))] w-56 shrink-0 overflow-y-auto py-10 xl:block">
        <Toc headings={doc.headings} />
      </aside>
    </div>
  );
}
