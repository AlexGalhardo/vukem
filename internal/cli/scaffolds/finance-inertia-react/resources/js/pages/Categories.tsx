import { useFinanceI18n } from "../lib/i18n";
import { Head, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../components/Field";
import { Card, Select, primary } from "../components/ui";
import type { Page } from "../page-props";

export default function Categories({ categories }: Page<"Categories">) {
  const {t}=useFinanceI18n();
  const form = useForm({ name: "", kind: "expense" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/categories", { onSuccess: () => form.reset("name") });
  };

  return (
    <>
      <Head title={t("categories")} />
      <h1 className="text-2xl font-semibold">{t("categories")}</h1>
      <div className="grid gap-4 sm:grid-cols-2">
        {(["expense", "income"] as const).map((kind) => (
          <Card key={kind} title={t(kind === "expense" ? "expenses" : "income")}>
            <ul className="flex flex-col gap-1">
              {categories
                .filter((c) => c.kind === kind)
                .map((c) => (
                  <li key={c.id}>{c.name}</li>
                ))}
            </ul>
          </Card>
        ))}
      </div>
      <Card title={t("newCategory")}>
        <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
          <Field label={t("name")} required value={form.data.name} onChange={(e) => form.setData("name", e.target.value)} error={form.errors.name} />
          <Select label={t("kind")} value={form.data.kind} onChange={(e) => form.setData("kind", e.target.value)} error={form.errors.kind}>
            <option value="expense">{t("expense")}</option>
            <option value="income">{t("income")}</option>
          </Select>
          <div className="self-end">
            <button type="submit" disabled={form.processing} className={primary}>
              Add category
            </button>
          </div>
        </form>
      </Card>
    </>
  );
}
