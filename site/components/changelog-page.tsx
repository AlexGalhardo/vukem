import { readChangelog, renderMarkdown } from "@/lib/changelog";
import { t } from "@/lib/ui-text";
import { site } from "@/lib/site";
import { extractHeadings } from "@/lib/toc";
import { Toc } from "@/components/toc";

export const changelogMetadata = {
  title: t.changelogTitle,
  description: t.changelogDescription,
};

export async function ChangelogPage(): Promise<React.ReactElement> {
  const { intro, body } = readChangelog();
  // Same slugs as rehype-slug, so the links match the release headings; [Unreleased] is not a version.
  const releases = extractHeadings(body)
    .filter((h) => h.depth === 2 && /^\[\d/.test(h.text))
    .map((h) => ({ ...h, text: h.text.replace(/^\[([^\]]+)\]\s+-\s+/, "$1 — ") }));
  return (
    <div className="mx-auto flex max-w-[90rem] justify-center gap-12 px-4 sm:px-6">
      <main id="content" className="min-w-0 w-full max-w-3xl py-10">
        <header className="mb-10">
          <h1 className="text-3xl font-semibold tracking-tight text-fg sm:text-[2.125rem]">{t.changelogTitle}</h1>
          <p className="mt-3 text-lg leading-relaxed text-muted">{t.changelogDescription}</p>
          <p className="mt-3 text-sm text-faint">
            <a href={`${site.repo}/blob/main/CHANGELOG.md`} target="_blank" rel="noreferrer" className="text-accent hover:underline">
              CHANGELOG.md
            </a>
          </p>
        </header>
        <article className="prose">
          {intro ? await renderMarkdown(intro) : null}
          {await renderMarkdown(body)}
        </article>
      </main>
      <aside className="sticky top-(--header-h) hidden h-[calc(100dvh-var(--header-h))] w-56 shrink-0 overflow-y-auto py-10 xl:block">
        <Toc headings={releases} label={t.releases} />
      </aside>
    </div>
  );
}
