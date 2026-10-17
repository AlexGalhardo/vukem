import type { Metadata } from "next";
import { readChangelog, renderMarkdown } from "@/lib/changelog";
import { dict, localeInfo, type Locale } from "@/lib/i18n";
import { site } from "@/lib/site";

export function changelogMetadata(locale: Locale): Metadata {
  const t = dict(locale);
  return {
    title: t.changelogTitle,
    description: t.changelogDescription,
    alternates: { canonical: locale === "en" ? "/changelog" : `/${locale}/changelog` },
  };
}

export async function ChangelogPage({ locale }: { locale: Locale }): Promise<React.ReactElement> {
  const t = dict(locale);
  const { intro, body } = readChangelog();
  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <main id="content">
        <header className="mb-10">
          <h1 className="text-3xl font-semibold tracking-tight text-fg sm:text-[2.125rem]">{t.changelogTitle}</h1>
          <p className="mt-3 text-lg leading-relaxed text-muted">{t.changelogDescription}</p>
          <p className="mt-3 text-sm text-faint">
            <a href={`${site.repo}/blob/main/CHANGELOG.md`} target="_blank" rel="noreferrer" className="text-accent hover:underline">
              CHANGELOG.md
            </a>
          </p>
        </header>
        {/* Release notes are written in English, like the rest of the repository. */}
        <article className="prose" lang={localeInfo.en.tag}>
          {intro ? await renderMarkdown(intro) : null}
          {await renderMarkdown(body)}
        </article>
      </main>
    </div>
  );
}
