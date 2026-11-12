import { Head, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../components/Field";
import { Card, Select, primary } from "../components/ui";
import type { Page } from "../page-props";

export default function Categories({ categories }: Page<"Categories">) {
  const form = useForm({ name: "", kind: "expense" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/categories", { onSuccess: () => form.reset("name") });
  };

  return (
    <>
      <Head title="Categories" />
      <h1 className="text-2xl font-semibold">Categories</h1>
      <div className="grid gap-4 sm:grid-cols-2">
        {(["expense", "income"] as const).map((kind) => (
          <Card key={kind} title={kind === "expense" ? "Expenses" : "Income"}>
            <ul className="space-y-1">
              {categories
                .filter((c) => c.kind === kind)
                .map((c) => (
                  <li key={c.id}>{c.name}</li>
                ))}
            </ul>
          </Card>
        ))}
      </div>
      <Card title="New category">
        <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
          <Field label="Name" required value={form.data.name} onChange={(e) => form.setData("name", e.target.value)} error={form.errors.name} />
          <Select label="Kind" value={form.data.kind} onChange={(e) => form.setData("kind", e.target.value)} error={form.errors.kind}>
            <option value="expense">Expense</option>
            <option value="income">Income</option>
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
