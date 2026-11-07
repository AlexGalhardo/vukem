"use client";

import { useEffect, useState } from "react";
import { Collapsible } from "@base-ui/react/collapsible";
import { Link } from "@/components/link";
import { ChevronRight } from "lucide-react";
import type { NavGroup } from "@/lib/content";
import { t } from "@/lib/ui-text";
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
function useBrowserPath(): string {
  const [path, setPath] = useState("");
  useEffect(() => {
    const pathname = window.location.pathname;
    setPath(pathname.length > 1 ? pathname.replace(/\/$/, "") : pathname);
  }, []);
  return path;
}

export function Sidebar({
  groupsByVersion,
  pathname,
  collapsible = false,
  onNavigate,
}: {
  groupsByVersion: Record<string, NavGroup[]>;
  /** Page path without a trailing slash. The mobile drawer reads the browser when this is omitted. */
  pathname?: string;
  collapsible?: boolean;
  onNavigate?: () => void;
}) {
  const live = useBrowserPath();
  // trailingSlash adds a slash in the browser; nav hrefs have none.
  const path = pathname ?? live;
  const groups = groupsByVersion[versionOfPath(path).id] ?? [];

  return (
    <nav aria-label={t.documentation} className={`text-sm ${collapsible ? "space-y-1" : "space-y-6"}`}>
      {groups.map((group) => {
        const list = (
          <ul className="space-y-0.5">
            {group.items.map((item) => (
              <Item key={item.href} {...item} active={item.href === path} onNavigate={onNavigate} />
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

        const open = group.items.some((i) => i.href === path);
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
