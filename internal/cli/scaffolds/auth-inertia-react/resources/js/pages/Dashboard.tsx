import { Head, Link, usePage } from "@inertiajs/react";

import type { SharedProps } from "../types";

export default function Dashboard() {
  const user = usePage<SharedProps>().props.auth?.user;

  return (
    <>
      <Head title="Dashboard" />
      <h1 className="text-2xl font-semibold">Hello, {user?.name}</h1>
      {user?.email_verified_at ? (
        <p className="mt-2">Your email is verified.</p>
      ) : (
        <p className="mt-2">
          <Link href="/email/verify" className="text-indigo-600 hover:underline dark:text-indigo-400">
            Verify your email
          </Link>
        </p>
      )}
    </>
  );
}
