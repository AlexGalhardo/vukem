import { Head, Link } from "@inertiajs/react";

// Rendered by the server for errors on Inertia visits (404, 403, 500…).
// message is safe to show: 5xx only ever carry the status text.
export default function ErrorPage({ status, message }: { status: number; message: string }) {
  return (
    <>
      <Head title={`${status} ${message}`} />
      <section className="py-12 text-center">
        <p className="text-sm font-semibold text-indigo-600 dark:text-indigo-400">{status}</p>
        <h1 className="mt-2 text-3xl font-semibold tracking-tight">{message}</h1>
        <Link href="/" className="mt-6 inline-block text-indigo-600 hover:underline dark:text-indigo-400">
          Back to the home page
        </Link>
      </section>
    </>
  );
}
