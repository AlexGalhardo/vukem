import { Link, usePage } from "@inertiajs/react";
import type { ReactNode } from "react";

import type { SharedProps } from "../types";

export default function AppLayout({ children }: { children: ReactNode }) {
  const page = usePage<SharedProps>();
  const success = page.flash?.success;

  return (
    <>
      <header className="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
        <Link href="/" className="font-bold">
          {page.props.appName}
        </Link>
        <nav>
          <Link href="/posts" className="text-indigo-600 hover:underline dark:text-indigo-400">
            Posts
          </Link>
        </nav>
      </header>
      <main className="mx-auto max-w-2xl p-4">
        {typeof success === "string" && (
          <p role="status" className="mb-4 rounded-md bg-indigo-50 px-4 py-3 text-indigo-900 dark:bg-indigo-950 dark:text-indigo-100">
            {success}
          </p>
        )}
        {children}
      </main>
    </>
  );
}
