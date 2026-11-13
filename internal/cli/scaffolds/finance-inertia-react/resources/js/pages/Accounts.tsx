import { useFinanceI18n } from "../lib/i18n";
import { Head, Link, useForm, usePage } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../components/Field";
import { Card, Select, danger, primary } from "../components/ui";
import type { Page, SharedProps } from "../page-props";

export default function Accounts({ accounts, limits }: Page<"Accounts">) {
  const currency = usePage<SharedProps>().props.workspace?.current.currency ?? "USD";
  const {t,money:formatMoney}=useFinanceI18n();
  const form = useForm({ name: "", kind: "checking", opening: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/accounts", { onSuccess: () => form.reset() });
  };

  return (
    <>
      <Head title={t("accounts")} />
      <h1 className="text-2xl font-semibold">{t("accounts")}</h1>
      {limits.accounts >= 0 && (
        <p className="text-sm text-muted-foreground">
          The {limits.plan} plan allows {limits.accounts} account.{" "}
          <Link href="/billing" className="text-primary hover:underline">
            Upgrade
          </Link>{" "}
          for more.
        </p>
      )}
      <Card>
        <ul className="divide-y divide-border">
          {accounts.map((a) => (
            <li key={a.id} className="flex items-center justify-between py-2">
              <span>
                {a.name} <span className="text-sm text-muted-foreground">({a.kind}, opening {formatMoney(a.opening_cents, currency)})</span>
              </span>
              <Link href={`/accounts/${a.id}`} method="delete" as="button" className={danger}>
                {t("delete")}
              </Link>
            </li>
          ))}
        </ul>
      </Card>
      <Card title={t("newAccount")}>
        <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
          <Field label={t("name")} required value={form.data.name} onChange={(e) => form.setData("name", e.target.value)} error={form.errors.name} />
          <Select label={t("kind")} value={form.data.kind} onChange={(e) => form.setData("kind", e.target.value)} error={form.errors.kind}>
            <option value="checking">{t("checking")}</option>
            <option value="savings">{t("savings")}</option>
            <option value="card">{t("card")}</option>
            <option value="cash">{t("cash")}</option>
          </Select>
          <Field label={t("opening")} inputMode="decimal" placeholder="0.00" value={form.data.opening} onChange={(e) => form.setData("opening", e.target.value)} error={form.errors.opening} />
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
