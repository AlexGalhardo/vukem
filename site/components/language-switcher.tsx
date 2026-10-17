"use client";

import { usePathname } from "next/navigation";
import { dict, localeInfo, localePrefix, locales, splitLocale, type Locale } from "@/lib/i18n";
import { versionOfPath, currentVersion } from "@/lib/versions";

/**
 * The same page in another locale. Only the current docs are localized, so
 * a snapshot page (/docs/0.20/...) links to the other locale's introduction.
 */
function hrefIn(pathname: string, target: Locale): string {
  const { path } = splitLocale(pathname);
  const docsPage = path.startsWith("/docs/") && versionOfPath(path).id === currentVersion.id;
  return `${localePrefix(target)}${docsPage ? path : "/docs/introduction"}`;
}

/** Links to the current page in each locale; a plain <a> because each locale has its own root layout. */
export function LanguageSwitcher({ locale }: { locale: Locale }): React.ReactElement {
  const pathname = usePathname();
  return (
    <nav aria-label={dict(locale).language} className="flex items-center gap-0.5 rounded-full border border-border p-0.5 text-xs font-medium">
      {locales.map((l) => {
        const active = l === locale;
        return (
          <a
            key={l}
            href={hrefIn(pathname, l)}
            hrefLang={localeInfo[l].tag}
            lang={localeInfo[l].tag}
            aria-current={active ? "true" : undefined}
            title={localeInfo[l].name}
            className={`inline-flex h-7 items-center rounded-full px-2.5 transition-colors ${
              active ? "bg-hover text-fg" : "text-muted hover:text-fg"
            }`}
          >
            {/* Accessible name "EN English": keeps the visible label first (WCAG 2.5.3). */}
            {localeInfo[l].short}
            <span className="sr-only"> {localeInfo[l].name}</span>
          </a>
        );
      })}
    </nav>
  );
}
