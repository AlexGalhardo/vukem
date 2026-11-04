import { Head, Link, usePage } from "@inertiajs/react";
import { type ReactNode, useEffect, useState } from "react";

import { type DashboardData, getJSON, type NavItem } from "../lib/adminApi";

function useTheme(): [string, () => void] {
  const [theme, setTheme] = useState(() =>
    typeof document !== "undefined" && document.documentElement.classList.contains("dark") ? "dark" : "light",
  );
  const toggle = () => {
    const next = theme === "dark" ? "light" : "dark";
    document.documentElement.classList.toggle("dark", next === "dark");
    try {
      localStorage.setItem("theme", next);
    } catch {
      // storage can be blocked; the choice then lasts for the page only
    }
    setTheme(next);
  };
  return [theme, toggle];
}

/** Sidebar dashboard shell: the resource list comes from the panel's JSON dashboard. */
export default function AdminLayout({ children, title }: { children: ReactNode; title: string }) {
  const { dataUrl } = usePage<{ dataUrl: string }>().props;
  const [nav, setNav] = useState<NavItem[]>([]);
  const [name, setName] = useState("Admin");
  const [theme, toggleTheme] = useTheme();

  useEffect(() => {
    const ctl = new AbortController();
    getJSON<DashboardData>(dataUrl, ctl.signal)
      .then((d) => {
        setNav(d.panel.nav ?? []);
        setName(d.panel.title);
      })
      .catch(() => {});
    return () => ctl.abort();
  }, [dataUrl]);

  return (
    <div className="min-h-screen bg-white text-zinc-900 dark:bg-zinc-950 dark:text-zinc-100">
      <Head title={`${title} · ${name}`} />
      <aside className="fixed inset-y-0 left-0 hidden w-60 border-r border-zinc-200 p-4 dark:border-zinc-800 lg:block">
        <Link href="/admin" className="mb-6 block text-lg font-semibold">
          {name}
        </Link>
        <nav aria-label="Admin" className="space-y-1">
          {nav.map((item) => (
            <Link
              key={item.url}
              href={`/admin/${item.url.replace(`${dataUrl}/`, "")}`}
              className="block rounded-md px-3 py-2 text-sm text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-900"
            >
              {item.label}
            </Link>
          ))}
        </nav>
      </aside>
      <div className="lg:pl-60">
        <header className="flex h-14 items-center justify-between border-b border-zinc-200 px-4 dark:border-zinc-800">
          <h1 className="text-base font-medium">{title}</h1>
          <button
            type="button"
            onClick={toggleTheme}
            className="rounded-md border border-zinc-200 px-3 py-1 text-sm hover:bg-zinc-100 dark:border-zinc-800 dark:hover:bg-zinc-900"
          >
            {theme === "dark" ? "Light" : "Dark"}
          </button>
        </header>
        <main className="p-4 lg:p-8">{children}</main>
      </div>
    </div>
  );
}
