import { Head, Link } from "@inertiajs/react";

export default function Home() {
  return (
    <>
      <Head title="Welcome" />
      <section className="py-12 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">Your Vukem app is running</h1>
        <p className="mt-3 text-zinc-600 dark:text-zinc-400">
          Go on the server, React with Inertia in the browser, Tailwind for the styles.
        </p>
        <Link
          href="/posts"
          className="mt-6 inline-block rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500"
        >
          See the posts
        </Link>
      </section>
    </>
  );
}
