import { Head, Link } from "@inertiajs/react";

import { primary, secondary } from "../components/ui";

export default function Home() {
  return (
    <>
      <Head title="Personal finances" />
      <section className="py-12 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">Know where your money goes</h1>
        <p className="mx-auto mt-3 max-w-xl text-zinc-600 dark:text-zinc-400">
          Accounts, transactions, monthly budgets and charts for you and the people you share money with. Import your bank statement as a CSV.
        </p>
        <div className="mt-6 flex justify-center gap-3">
          <Link href="/register" className={primary}>
            Create an account
          </Link>
          <Link href="/login" className={secondary}>
            Log in
          </Link>
        </div>
      </section>
    </>
  );
}
