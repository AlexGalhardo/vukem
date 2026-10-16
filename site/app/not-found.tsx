import Link from "next/link";

export default function NotFound() {
  return (
    <main id="content" className="mx-auto max-w-xl px-4 py-24 text-center">
      <p className="text-sm font-medium text-accent">404</p>
      <h1 className="mt-2 text-3xl font-semibold tracking-tight">Page not found</h1>
      <p className="mt-3 text-muted">The page you are looking for does not exist or has moved.</p>
      <Link href="/docs/introduction" className="mt-8 inline-flex h-10 items-center rounded-lg border border-border px-4 text-sm font-medium hover:bg-hover">
        Go to the introduction
      </Link>
    </main>
  );
}
