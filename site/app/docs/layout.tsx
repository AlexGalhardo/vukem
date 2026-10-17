import { Sidebar } from "@/components/sidebar";
import { getNavGroupsByVersion } from "@/lib/content";

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="mx-auto flex max-w-[90rem] px-4 sm:px-6">
      <aside
        aria-label="Sidebar"
        className="sticky top-(--header-h) hidden h-[calc(100dvh-var(--header-h))] w-64 shrink-0 overflow-y-auto py-8 pr-6 lg:block"
      >
        <Sidebar groupsByVersion={getNavGroupsByVersion()} />
      </aside>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
