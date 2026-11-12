import { Head, Link, router, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import { Card, Select, primary } from "../components/ui";
import type { Page } from "../page-props";

type PreviewProps = NonNullable<Page<"Import">["preview"]>;

// guess picks the header that looks like what the column is for.
function guess(headers: string[], words: string[]): string {
  return headers.find((h) => words.some((w) => h.toLowerCase().includes(w))) ?? headers[0] ?? "";
}

function Mapping({ preview, dateFormats }: { preview: PreviewProps; dateFormats: string[] }) {
  const form = useForm({
    date: guess(preview.headers, ["date", "data"]),
    description: guess(preview.headers, ["descr", "memo", "histor", "name"]),
    amount: guess(preview.headers, ["amount", "valor", "value"]),
    category: "",
    dateFormat: dateFormats[0] ?? "2006-01-02",
    invert: "false",
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post(`/import/${preview.importId}`);
  };

  const columns = preview.headers.map((h) => (
    <option key={h} value={h}>
      {h}
    </option>
  ));

  return (
    <Card title={`Map the columns of ${preview.filename}`}>
      <div className="mb-4 overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead className="text-zinc-500">
            <tr>
              {preview.headers.map((h) => (
                <th key={h} className="pr-4">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {preview.rows.map((row, i) => (
              <tr key={i}>
                {row.map((cell, j) => (
                  <td key={j} className="pr-4">
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
        <Select label="Date column" value={form.data.date} onChange={(e) => form.setData("date", e.target.value)} error={form.errors.date}>
          {columns}
        </Select>
        <Select label="Description column" value={form.data.description} onChange={(e) => form.setData("description", e.target.value)} error={form.errors.description}>
          {columns}
        </Select>
        <Select label="Amount column" value={form.data.amount} onChange={(e) => form.setData("amount", e.target.value)} error={form.errors.amount}>
          {columns}
        </Select>
        <Select label="Category column" value={form.data.category} onChange={(e) => form.setData("category", e.target.value)}>
          <option value="">None</option>
          {columns}
        </Select>
        <Select label="Date format" value={form.data.dateFormat} onChange={(e) => form.setData("dateFormat", e.target.value)}>
          {dateFormats.map((f) => (
            <option key={f} value={f}>
              {f.replace("2006", "YYYY").replace("01", "MM").replace("02", "DD")}
            </option>
          ))}
        </Select>
        <label className="flex items-end gap-2 pb-2">
          <input type="checkbox" checked={form.data.invert === "true"} onChange={(e) => form.setData("invert", String(e.target.checked))} />
          Positive amounts are expenses
        </label>
        <div className="sm:col-span-3">
          <button type="submit" disabled={form.processing} className={primary}>
            Import
          </button>
        </div>
      </form>
    </Card>
  );
}

export default function Import({ accounts, imports, preview, dateFormats, enabled }: Page<"Import">) {
  const form = useForm<{ accountId: string; file: File | null }>({ accountId: String(accounts[0]?.id ?? ""), file: null });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/import", { forceFormData: true });
  };

  return (
    <>
      <Head title="Import" />
      <h1 className="text-2xl font-semibold">Import a bank statement</h1>
      {!enabled && (
        <p className="rounded-md bg-amber-50 px-4 py-3 text-amber-900 dark:bg-amber-950 dark:text-amber-100">
          CSV import is a Pro feature.{" "}
          <Link href="/billing" className="font-medium underline">
            Start the 14-day trial
          </Link>
          .
        </p>
      )}
      {preview ? (
        <Mapping preview={preview} dateFormats={dateFormats} />
      ) : (
        <Card title="Upload a CSV">
          <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
            <Select label="Account" value={form.data.accountId} onChange={(e) => form.setData("accountId", e.target.value)} error={form.errors.accountId}>
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </Select>
            <label className="block sm:col-span-2">
              <span className="font-medium">CSV file (up to 2 MiB)</span>
              <input type="file" accept=".csv,text/csv" required className="mt-1 block w-full" onChange={(e) => form.setData("file", e.target.files?.[0] ?? null)} />
            </label>
            <div className="sm:col-span-3">
              <button type="submit" disabled={form.processing || !form.data.file} className={primary}>
                Upload
              </button>
            </div>
          </form>
        </Card>
      )}
      <Card title="Latest imports">
        {imports.length === 0 ? (
          <p className="text-zinc-500">Nothing imported yet.</p>
        ) : (
          <ul className="divide-y divide-zinc-200 dark:divide-zinc-800">
            {imports.map((i) => (
              <li key={i.id} className="py-2 text-sm">
                <strong>{i.filename}</strong>: {i.status}
                {i.status === "done" && `, ${i.imported} imported, ${i.failed} skipped`}
                {i.error && <pre className="mt-1 whitespace-pre-wrap text-xs text-zinc-500">{i.error}</pre>}
              </li>
            ))}
          </ul>
        )}
        <button type="button" className="mt-3 text-sm text-indigo-600 hover:underline dark:text-indigo-400" onClick={() => router.reload({ only: ["imports"] })}>
          Refresh
        </button>
      </Card>
    </>
  );
}
