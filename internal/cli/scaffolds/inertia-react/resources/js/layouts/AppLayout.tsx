import { Link, usePage } from "@inertiajs/react";
import type { ReactNode } from "react";

import { demoInstalled, demoLink } from "../demo";
import type { SharedProps } from "../types";

const link = "text-indigo-600 hover:underline dark:text-indigo-400";

export default function AppLayout({ children }: { children: ReactNode }) {
  const page = usePage<SharedProps>();
  const { auth } = page.props;
  const message = page.flash?.success ?? page.flash?.status;

  return (
    <>
      <header className="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
        <Link href="/" className="font-bold">
          {page.props.appName}
        </Link>
        <nav className="flex items-center gap-4">
          {demoInstalled && (
            <Link href={demoLink.href} className={link}>
              {demoLink.label}
            </Link>
          )}
          {auth?.user ? (
            <>
              <Link href="/dashboard" className={link}>
                {auth.user.name}
              </Link>
              <Link href="/logout" method="post" as="button" className={link}>
                Log out
              </Link>
            </>
          ) : auth ? (
            <>
              <Link href="/login" className={link}>
                Log in
              </Link>
              <Link href="/register" className={link}>
                Register
              </Link>
            </>
          ) : null}
        </nav>
      </header>
      <main className="mx-auto max-w-2xl p-4">
        {typeof message === "string" && (
          <p role="status" className="mb-4 rounded-md bg-indigo-50 px-4 py-3 text-indigo-900 dark:bg-indigo-950 dark:text-indigo-100">
            {message}
          </p>
        )}
        {children}
      </main>
    </>
  );
}
