"use client";

import { usePathname, useRouter } from "next/navigation";
import { ChevronDown } from "lucide-react";
import { docsBase, pageOfPath, versionOfPath, versions } from "@/lib/versions";

/**
 * Docs version picker. Switching keeps the current page when the target
 * version has it, otherwise it opens that version's introduction.
 */
export function VersionSwitcher({ hrefsByVersion }: { hrefsByVersion: Record<string, string[]> }) {
  const pathname = usePathname();
  const router = useRouter();
  const active = versionOfPath(pathname);

  function select(id: string): void {
    const target = versions.find((v) => v.id === id);
    if (!target || target.id === active.id) return;
    const same = `${docsBase(target)}${pageOfPath(pathname)}`;
    router.push(hrefsByVersion[target.id]?.includes(same) ? same : `${docsBase(target)}/introduction`);
  }

  return (
    <label className="relative inline-flex items-center">
      <span className="sr-only">Documentation version</span>
      <select
        value={active.id}
        onChange={(e) => select(e.target.value)}
        className="h-7 cursor-pointer appearance-none rounded-md border border-border bg-bg py-0 pr-6 pl-2 text-xs font-medium text-muted transition-colors hover:border-border-strong hover:text-fg focus-visible:outline-2 focus-visible:outline-accent"
      >
        {versions.map((v, i) => (
          <option key={v.id} value={v.id}>
            {i === 0 ? `${v.label} (latest)` : v.label}
          </option>
        ))}
      </select>
      <ChevronDown aria-hidden className="pointer-events-none absolute right-1.5 size-3 text-faint" />
    </label>
  );
}
