import { Head, Link, useForm, usePage } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../components/Field";
import { Card, Select, danger, primary } from "../components/ui";
import { formatMoney } from "../lib/money";
import type { Page, SharedProps } from "../page-props";

export default function Accounts({ accounts, limits }: Page<"Accounts">) {
  const currency = usePage<SharedProps>().props.workspace?.current.currency ?? "USD";
  const form = useForm({ name: "", kind: "checking", opening: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/accounts", { onSuccess: () => form.reset() });
  };

  return (
    <>
      <Head title="Accounts" />
      <h1 className="text-2xl font-semibold">Accounts</h1>
      {limits.accounts >= 0 && (
        <p className="text-sm text-zinc-500">
          The {limits.plan} plan allows {limits.accounts} account.{" "}
          <Link href="/billing" className="text-indigo-600 hover:underline dark:text-indigo-400">
            Upgrade
          </Link>{" "}
          for more.
        </p>
      )}
      <Card>
        <ul className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {accounts.map((a) => (
            <li key={a.id} className="flex items-center justify-between py-2">
              <span>
                {a.name} <span className="text-sm text-zinc-500">({a.kind}, opening {formatMoney(a.opening_cents, currency)})</span>
              </span>
              <Link href={`/accounts/${a.id}`} method="delete" as="button" className={danger}>
                Delete
              </Link>
            </li>
          ))}
        </ul>
      </Card>
      <Card title="New account">
        <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
          <Field label="Name" required value={form.data.name} onChange={(e) => form.setData("name", e.target.value)} error={form.errors.name} />
          <Select label="Kind" value={form.data.kind} onChange={(e) => form.setData("kind", e.target.value)} error={form.errors.kind}>
            <option value="checking">Checking</option>
            <option value="savings">Savings</option>
            <option value="card">Card</option>
            <option value="cash">Cash</option>
          </Select>
          <Field label="Opening balance" inputMode="decimal" placeholder="0.00" value={form.data.opening} onChange={(e) => form.setData("opening", e.target.value)} error={form.errors.opening} />
          <div className="sm:col-span-3">
            <button type="submit" disabled={form.processing} className={primary}>
              Add account
            </button>
          </div>
        </form>
      </Card>
    </>
  );
}
