import { Head, Link, usePage } from "@inertiajs/react";

import type { SharedProps } from "../../types";

export default function VerifyEmail() {
  const { auth } = usePage<SharedProps>().props;

  return (
    <>
      <Head title="Verify your email" />
      <h1 className="mb-2 text-2xl font-semibold">Verify your email</h1>
      <p className="mb-4">
        We sent a link to {auth?.user?.email}. Open it to verify your address.
      </p>
      <Link
        href="/email/verification-notification"
        method="post"
        as="button"
        className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500"
      >
        Send the link again
      </Link>
    </>
  );
}
