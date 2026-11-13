import { useFinanceI18n } from "../lib/i18n";
import { Head, Link, useForm, usePage } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../components/Field";
import { Card, Select, danger, primary } from "../components/ui";
import type { Page, SharedProps } from "../page-props";

export default function Budgets({ budgets, categories, month, enabled }: Page<"Budgets">) {
  const currency = usePage<SharedProps>().props.workspace?.current.currency ?? "USD";
  const expense = categories.filter((c) => c.kind === "expense");
  const {t,money:formatMoney,month:monthLabel}=useFinanceI18n();
  const form = useForm({ categoryId: String(expense[0]?.id ?? ""), limit: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/budgets", { onSuccess: () => form.reset("limit") });
  };

  return (
    <>
      <Head title={t("budgets")} />
      <h1 className="text-2xl font-semibold">{t("budgets")} · {monthLabel(month)}</h1>
      {!enabled && (
        <p className="rounded-md bg-amber-50 px-4 py-3 text-amber-900 dark:bg-amber-950 dark:text-amber-100">
          Budgets are a Pro feature.{" "}
          <Link href="/billing" className="font-medium underline">
            Start the 14-day trial
          </Link>
          .
        </p>
      )}
      <Card>
        {budgets.length === 0 ? (
          <p className="text-muted-foreground">No budgets yet. You get an email when a category reaches 80% and 100%.</p>
        ) : (
          <ul className="divide-y divide-border">
            {budgets.map((b) => (
              <li key={b.id} className="flex items-center justify-between py-2">
                <span>
                  {b.category}: {formatMoney(b.spentCents, currency)} {t("of")} {formatMoney(b.limitCents, currency)} ({b.percent}%)
                </span>
                <Link href={`/budgets/${b.id}`} method="delete" as="button" className={danger}>
                  Remove
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <Card title={t("setBudget")}>
        <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
          <Select label={t("category")} value={form.data.categoryId} onChange={(e) => form.setData("categoryId", e.target.value)} error={form.errors.categoryId}>
            {expense.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
          <Field label={t("monthlyLimit")} inputMode="decimal" placeholder="300.00" required value={form.data.limit} onChange={(e) => form.setData("limit", e.target.value)} error={form.errors.limit} />
          <div className="self-end">
            <button type="submit" disabled={form.processing} className={primary}>
              Save budget
            </button>
          </div>
        </form>
      </Card>
    </>
  );
}
