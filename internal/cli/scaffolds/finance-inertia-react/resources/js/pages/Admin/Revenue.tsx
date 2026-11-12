import { Head, Link } from "@inertiajs/react";

import { Card } from "../../components/ui";
import { formatMoney } from "../../lib/money";
import type { Page } from "../../page-props";

export default function Revenue({ mrrCents, active, trialing, pastDue, workspaces, users }: Page<"Admin/Revenue">) {
  const stats: [string, string][] = [
    ["MRR (active subscriptions)", formatMoney(mrrCents, "USD")],
    ["Active", String(active)],
    ["Trialing", String(trialing)],
    ["Past due", String(pastDue)],
    ["Workspaces", String(workspaces)],
    ["Users", String(users)],
  ];

  return (
    <>
      <Head title="Revenue" />
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">Revenue</h1>
        <Link href="/admin" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Admin panel
        </Link>
      </div>
      <div className="grid gap-4 sm:grid-cols-3">
        {stats.map(([label, value]) => (
          <Card key={label} title={label}>
            <p className="text-2xl font-semibold">{value}</p>
          </Card>
        ))}
      </div>
      <p className="text-sm text-zinc-500">MRR sums the monthly unit price of the active subscriptions, in USD. Trials count once they convert.</p>
    </>
  );
}
