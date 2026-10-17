import { Logo } from "@/components/logo";
import { MobileNav } from "@/components/mobile-nav";
import { SearchDialog } from "@/components/search-dialog";
import { ThemeToggle } from "@/components/theme-toggle";
import { IconTooltip } from "@/components/icon-tooltip";
import { VersionSwitcher } from "@/components/version-switcher";
import { getNavGroupsByVersion } from "@/lib/content";
import { site } from "@/lib/site";

function GitHubIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden className={className}>
      <path d="M12 .5C5.65.5.5 5.65.5 12a11.5 11.5 0 0 0 7.86 10.92c.58.1.79-.25.79-.56v-2c-3.2.7-3.87-1.37-3.87-1.37-.53-1.33-1.28-1.69-1.28-1.69-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.55-.29-5.24-1.28-5.24-5.68 0-1.26.45-2.28 1.19-3.09-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.17 1.18a11 11 0 0 1 5.77 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.11 3.05.74.81 1.19 1.83 1.19 3.09 0 4.41-2.69 5.39-5.25 5.67.41.36.78 1.06.78 2.14v3.17c0 .31.21.67.8.56A11.5 11.5 0 0 0 23.5 12C23.5 5.65 18.35.5 12 .5Z" />
    </svg>
  );
}

function hrefsOf(groupsByVersion: Record<string, { items: { href: string }[] }[]>): Record<string, string[]> {
  return Object.fromEntries(
    Object.entries(groupsByVersion).map(([id, groups]) => [id, groups.flatMap((g) => g.items.map((i) => i.href))]),
  );
}

export function Header() {
  const groupsByVersion = getNavGroupsByVersion();
  return (
    <header className="sticky top-0 z-40 h-(--header-h) border-b border-border bg-bg/80 backdrop-blur-md supports-[backdrop-filter]:bg-bg/70">
      {/* Same container as app/docs/layout.tsx; the left column matches the sidebar width. */}
      <div className="mx-auto flex h-full max-w-[90rem] items-center gap-3 px-4 sm:px-6 lg:grid lg:grid-cols-[16rem_1fr_16rem] lg:gap-0">
        <div className="flex items-center gap-3">
          <MobileNav groupsByVersion={groupsByVersion} />
          <Logo />
          <VersionSwitcher hrefsByVersion={hrefsOf(groupsByVersion)} />
        </div>
        <div className="flex flex-1 justify-end sm:justify-center">
          <SearchDialog />
        </div>
        <div className="flex items-center justify-end gap-2">
          <IconTooltip label="GitHub repository">
            <a
              href={site.repo}
              target="_blank"
              rel="noreferrer"
              aria-label="Vukem on GitHub"
              className="inline-flex size-8 items-center justify-center rounded-lg text-muted transition-colors hover:bg-hover hover:text-fg"
            >
              <GitHubIcon className="size-4" />
            </a>
          </IconTooltip>
          <ThemeToggle />
        </div>
      </div>
    </header>
  );
}
