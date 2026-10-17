"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Collapsible } from "@base-ui/react/collapsible";
import { ChevronRight } from "lucide-react";
import type { NavGroup } from "@/lib/content";
import { dict, splitLocale, type Locale } from "@/lib/i18n";
import { versionOfPath } from "@/lib/versions";

function Item({ href, title, active, onNavigate }: { href: string; title: string; active: boolean; onNavigate?: () => void }) {
  return (
    <li>
      <Link
        href={href}
        onClick={onNavigate}
        aria-current={active ? "page" : undefined}
        className={`block rounded-lg px-3 py-1.5 text-sm transition-colors ${
          active ? "bg-accent-soft font-medium text-accent" : "text-muted hover:bg-hover hover:text-fg"
        }`}
      >
        {title}
      </Link>
    </li>
  );
}

/**
 * Category-grouped page list. On mobile (`collapsible`) each group is a
 * Base UI Collapsible, open when it contains the current page. Shows the
 * groups of the docs version the current pathname belongs to.
 */
export function Sidebar({
  groupsByVersion,
  locale,
  collapsible = false,
  onNavigate,
}: {
  groupsByVersion: Record<string, NavGroup[]>;
  locale: Locale;
  collapsible?: boolean;
  onNavigate?: () => void;
}) {
  const pathname = usePathname();
  const groups = groupsByVersion[versionOfPath(splitLocale(pathname).path).id] ?? [];

  return (
    <nav aria-label={dict(locale).documentation} className={`text-sm ${collapsible ? "space-y-1" : "space-y-6"}`}>
      {groups.map((group) => {
        const list = (
          <ul className="space-y-0.5">
            {group.items.map((item) => (
              <Item key={item.href} {...item} active={item.href === pathname} onNavigate={onNavigate} />
            ))}
          </ul>
        );

        if (!collapsible) {
          return (
            <div key={group.title}>
              <p className="mb-2 px-3 text-xs font-semibold tracking-wide text-fg">{group.title}</p>
              {list}
            </div>
          );
        }

        const open = group.items.some((i) => i.href === pathname);
        return (
          <Collapsible.Root key={group.title} defaultOpen={open}>
            <Collapsible.Trigger className="group flex w-full items-center justify-between rounded-lg px-3 py-1.5 text-xs font-semibold tracking-wide text-fg hover:bg-hover">
              {group.title}
              <ChevronRight aria-hidden className="size-3.5 text-faint transition-transform group-data-panel-open:rotate-90" />
            </Collapsible.Trigger>
            <Collapsible.Panel className="mt-1">{list}</Collapsible.Panel>
          </Collapsible.Root>
        );
      })}
    </nav>
  );
}
