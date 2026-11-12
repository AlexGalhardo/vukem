import { Link, router, usePage } from "@inertiajs/react";
import type { ReactNode } from "react";

import type { SharedProps } from "../page-props";

const link = "text-indigo-600 hover:underline dark:text-indigo-400";

const nav = [
  ["/dashboard", "Dashboard"],
  ["/transactions", "Transactions"],
  ["/accounts", "Accounts"],
  ["/categories", "Categories"],
  ["/budgets", "Budgets"],
  ["/import", "Import"],
  ["/workspace", "Workspace"],
  ["/billing", "Billing"],
] as const;

export default function AppLayout({ children }: { children: ReactNode }) {
  const page = usePage<SharedProps>();
  const { auth, workspace } = page.props;
  const success = page.flash?.success ?? page.flash?.status;
  const error = page.flash?.error;

  return (
    <>
      <header className="border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3">
          <Link href={auth?.user ? "/dashboard" : "/"} className="font-bold">
            {page.props.appName}
          </Link>
          {auth?.user ? (
            <div className="flex flex-wrap items-center gap-4">
              {workspace && (
                <label className="flex items-center gap-2 text-sm">
                  <span className="sr-only">Workspace</span>
                  <select
                    value={workspace.current.id}
                    onChange={(e) => router.post(`/workspaces/${e.target.value}/switch`)}
                    className="rounded-md border border-zinc-300 bg-transparent px-2 py-1 dark:border-zinc-700"
                  >
                    {workspace.all.map((w) => (
                      <option key={w.id} value={w.id}>
                        {w.name}
                      </option>
                    ))}
                  </select>
                  <span className="rounded bg-indigo-100 px-2 py-0.5 text-xs font-medium uppercase text-indigo-800 dark:bg-indigo-950 dark:text-indigo-200">
                    {workspace.plan}
                  </span>
                </label>
              )}
              {workspace?.admin && (
                <Link href="/admin" className={link}>
                  Admin
                </Link>
              )}
              <Link href="/user/two-factor" className={link}>
                {auth.user.name}
              </Link>
              <Link href="/logout" method="post" as="button" className={link}>
                Log out
              </Link>
            </div>
          ) : (
            <div className="flex items-center gap-4">
              <Link href="/login" className={link}>
                Log in
              </Link>
              <Link href="/register" className={link}>
                Register
              </Link>
            </div>
          )}
        </div>
        {auth?.user && workspace && (
          <nav className="mx-auto mt-3 flex max-w-5xl flex-wrap gap-4 text-sm">
            {nav.map(([href, label]) => (
              <Link key={href} href={href} className={link}>
                {label}
              </Link>
            ))}
          </nav>
        )}
      </header>
      <main className="mx-auto max-w-5xl space-y-4 p-4">
        {typeof success === "string" && (
          <p role="status" className="rounded-md bg-indigo-50 px-4 py-3 text-indigo-900 dark:bg-indigo-950 dark:text-indigo-100">
            {success}
          </p>
        )}
        {typeof error === "string" && (
          <p role="alert" className="rounded-md bg-red-50 px-4 py-3 text-red-900 dark:bg-red-950 dark:text-red-100">
            {error}
          </p>
        )}
        {children}
      </main>
    </>
  );
}
