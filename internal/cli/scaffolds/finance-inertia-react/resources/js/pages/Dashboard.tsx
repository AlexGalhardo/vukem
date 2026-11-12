import { Head, Link } from "@inertiajs/react";

import BarChart from "../components/charts/BarChart";
import DonutChart from "../components/charts/DonutChart";
import { Card } from "../components/ui";
import { formatMoney, monthLabel } from "../lib/money";
import type { Page } from "../page-props";

export default function Dashboard({ dashboard }: Page<"Dashboard">) {
  const { currency, month, balanceCents, months, categories, budgets } = dashboard;
  const current = months[months.length - 1];

  return (
    <>
      <Head title="Dashboard" />
      <div className="flex items-baseline justify-between">
        <h1 className="text-2xl font-semibold">Dashboard</h1>
        <span className="text-zinc-500">{monthLabel(month)}</span>
      </div>
      <div className="grid gap-4 sm:grid-cols-3">
        <Card title="Balance">
          <p className="text-2xl font-semibold">{formatMoney(balanceCents, currency)}</p>
        </Card>
        <Card title="Income this month">
          <p className="text-2xl font-semibold text-emerald-600">{formatMoney(current?.incomeCents ?? 0, currency)}</p>
        </Card>
        <Card title="Expenses this month">
          <p className="text-2xl font-semibold text-rose-600">{formatMoney(current?.expenseCents ?? 0, currency)}</p>
        </Card>
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Card title="Income vs expenses">
          <BarChart points={months} currency={currency} />
        </Card>
        <Card title="Spending by category">
          <DonutChart slices={categories} currency={currency} />
        </Card>
      </div>
      <Card title="Budgets">
        {budgets.length === 0 ? (
          <p className="text-zinc-500">
            No budgets yet.{" "}
            <Link href="/budgets" className="text-indigo-600 hover:underline dark:text-indigo-400">
              Set one
            </Link>{" "}
            to get an email at 80% and 100%.
          </p>
        ) : (
          <ul className="space-y-3">
            {budgets.map((b) => (
              <li key={b.id}>
                <div className="flex justify-between text-sm">
                  <span>{b.category}</span>
                  <span>
                    {formatMoney(b.spentCents, currency)} of {formatMoney(b.limitCents, currency)}
                  </span>
                </div>
                <div className="mt-1 h-2 rounded bg-zinc-200 dark:bg-zinc-800">
                  <div
                    className={`h-2 rounded ${b.percent >= 100 ? "bg-red-500" : b.percent >= 80 ? "bg-amber-500" : "bg-emerald-500"}`}
                    style={{ width: `${Math.min(b.percent, 100)}%` }}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  );
}
