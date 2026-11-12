import { Head, Link, router, useForm, usePage } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../components/Field";
import { Card, Select, danger, primary, secondary } from "../components/ui";
import { formatMoney } from "../lib/money";
import type { Page, SharedProps } from "../page-props";

export default function Transactions({ transactions, accounts, categories, filters, today }: Page<"Transactions">) {
  const currency = usePage<SharedProps>().props.workspace?.current.currency ?? "USD";
  const form = useForm({
    accountId: String(accounts[0]?.id ?? ""),
    categoryId: "",
    kind: "expense",
    amount: "",
    description: "",
    date: today,
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/transactions", { onSuccess: () => form.reset("amount", "description") });
  };

  const filter = (name: string, value: string) => {
    const next = { ...filters, [name]: value };
    router.get("/transactions", Object.fromEntries(Object.entries(next).filter(([, v]) => v)), {
      preserveState: true,
      replace: true,
    });
  };

  const kindCategories = categories.filter((c) => c.kind === form.data.kind);

  return (
    <>
      <Head title="Transactions" />
      <h1 className="text-2xl font-semibold">Transactions</h1>
      <Card title="New transaction">
        <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
          <Select label="Kind" value={form.data.kind} onChange={(e) => form.setData({ ...form.data, kind: e.target.value, categoryId: "" })} error={form.errors.kind}>
            <option value="expense">Expense</option>
            <option value="income">Income</option>
          </Select>
          <Field label="Amount" inputMode="decimal" placeholder="12.50" required value={form.data.amount} onChange={(e) => form.setData("amount", e.target.value)} error={form.errors.amount} />
          <Field label="Date" type="date" required value={form.data.date} onChange={(e) => form.setData("date", e.target.value)} error={form.errors.date} />
          <Select label="Account" value={form.data.accountId} onChange={(e) => form.setData("accountId", e.target.value)} error={form.errors.accountId}>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </Select>
          <Select label="Category" value={form.data.categoryId} onChange={(e) => form.setData("categoryId", e.target.value)} error={form.errors.categoryId}>
            <option value="">None</option>
            {kindCategories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
          <Field label="Description" value={form.data.description} onChange={(e) => form.setData("description", e.target.value)} error={form.errors.description} />
          <div className="sm:col-span-3">
            <button type="submit" disabled={form.processing} className={primary}>
              Add
            </button>
          </div>
        </form>
      </Card>
      <Card>
        <div className="mb-4 grid gap-3 sm:grid-cols-5">
          <Field label="From" type="date" value={filters.from} onChange={(e) => filter("from", e.target.value)} />
          <Field label="To" type="date" value={filters.to} onChange={(e) => filter("to", e.target.value)} />
          <Select label="Category" value={filters.categoryId} onChange={(e) => filter("categoryId", e.target.value)}>
            <option value="">Any</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
          <Select label="Account" value={filters.accountId} onChange={(e) => filter("accountId", e.target.value)}>
            <option value="">Any</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </Select>
          <Select label="Kind" value={filters.kind} onChange={(e) => filter("kind", e.target.value)}>
            <option value="">Any</option>
            <option value="expense">Expense</option>
            <option value="income">Income</option>
          </Select>
        </div>
        {transactions.length === 0 ? (
          <p className="text-zinc-500">
            No transactions match.{" "}
            <Link href="/import" className="text-indigo-600 hover:underline dark:text-indigo-400">
              Import a statement
            </Link>{" "}
            or add one above.
          </p>
        ) : (
          <table className="w-full text-left text-sm">
            <thead className="text-zinc-500">
              <tr>
                <th className="py-2">Date</th>
                <th>Description</th>
                <th>Category</th>
                <th>Account</th>
                <th className="text-right">Amount</th>
                <th />
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-200 dark:divide-zinc-800">
              {transactions.map((t) => (
                <tr key={t.id}>
                  <td className="py-2">{t.occurred_on}</td>
                  <td>{t.description}</td>
                  <td>{t.categoryName}</td>
                  <td>{t.accountName}</td>
                  <td className={`text-right ${t.kind === "income" ? "text-emerald-600" : "text-rose-600"}`}>
                    {t.kind === "income" ? "+" : "-"}
                    {formatMoney(t.amount_cents, currency)}
                  </td>
                  <td className="text-right">
                    <Link href={`/transactions/${t.id}`} method="delete" as="button" className={danger}>
                      Delete
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <button type="button" className={`${secondary} mt-4`} onClick={() => router.get("/transactions")}>
          Clear filters
        </button>
      </Card>
    </>
  );
}
