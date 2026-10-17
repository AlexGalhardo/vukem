import { Sidebar } from "@/components/sidebar";
import { getNavGroupsByVersion } from "@/lib/content";
import type { Locale } from "@/lib/i18n";

/** Sidebar + content columns shared by /docs and /<locale>/docs. */
export function DocsFrame({ locale, children }: { locale: Locale; children: React.ReactNode }): React.ReactElement {
  return (
    <div className="mx-auto flex max-w-[90rem] px-4 sm:px-6">
      <aside
        aria-label="Sidebar"
        className="sticky top-(--header-h) hidden h-[calc(100dvh-var(--header-h))] w-64 shrink-0 overflow-y-auto py-8 pr-6 lg:block"
      >
        <Sidebar groupsByVersion={getNavGroupsByVersion(locale)} locale={locale} />
      </aside>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
