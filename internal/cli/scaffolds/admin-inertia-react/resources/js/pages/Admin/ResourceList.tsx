import { Link } from "@inertiajs/react";
import { type FormEvent, useEffect, useState } from "react";

import AdminLayout from "../../layouts/AdminLayout";
import { getJSON, type ListData } from "../../lib/adminApi";

export default function ResourceList({ slug, dataUrl }: { slug: string; dataUrl: string }) {
  const [data, setData] = useState<ListData | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState(() => (typeof window === "undefined" ? "" : window.location.search));

  useEffect(() => {
    const ctl = new AbortController();
    getJSON<ListData>(`${dataUrl}/${encodeURIComponent(slug)}${query}`, ctl.signal)
      .then((d) => {
        setData(d);
        setError("");
      })
      .catch((e: Error) => e.name !== "AbortError" && setError(e.message));
    return () => ctl.abort();
  }, [dataUrl, slug, query]);

  const go = (qs: URLSearchParams) => {
    const next = qs.toString() ? `?${qs.toString()}` : "";
    window.history.replaceState(null, "", `/admin/${encodeURIComponent(slug)}${next}`);
    setQuery(next);
  };
  const params = () => new URLSearchParams(query);

  const search = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const qs = params();
    const q = new FormData(e.currentTarget).get("q");
    if (q) qs.set("q", String(q));
    else qs.delete("q");
    qs.delete("page");
    go(qs);
  };
  const sortBy = (sortUrl?: string) => {
    if (sortUrl) go(new URL(sortUrl, window.location.origin).searchParams);
  };
  const page = (n: number) => {
    const qs = params();
    qs.set("page", String(n));
    go(qs);
  };

  const columns = data?.columns ?? [];

  return (
    <AdminLayout title={data?.resource.plural ?? slug}>
      {data?.searchable && (
        <form onSubmit={search} className="mb-4 flex gap-2">
          <input
            name="q"
            defaultValue={data.search}
            placeholder="Search"
            aria-label="Search"
            className="w-64 rounded-md border border-zinc-300 bg-transparent px-3 py-2 text-sm dark:border-zinc-700"
          />
          <button
            type="submit"
            className="rounded-md bg-zinc-900 px-3 py-2 text-sm text-white dark:bg-zinc-100 dark:text-zinc-900"
          >
            Search
          </button>
        </form>
      )}
      {error && (
        <p role="alert" className="text-red-600">
          {error}
        </p>
      )}
      {!data && !error && <p className="text-zinc-500">Loading…</p>}
      {data && (
        <div className="overflow-x-auto rounded-lg border border-zinc-200 dark:border-zinc-800">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-zinc-200 text-zinc-500 dark:border-zinc-800">
              <tr>
                {columns.map((col) => (
                  <th key={col.label} className="px-4 py-3 font-medium">
                    {col.sortUrl ? (
                      <button type="button" onClick={() => sortBy(col.sortUrl)} className="hover:underline">
                        {col.label}
                        {col.sorted ? ` ${col.sorted}` : ""}
                      </button>
                    ) : (
                      col.label
                    )}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(data.rows ?? []).map((row) => (
                <tr key={row.id} className="border-b border-zinc-100 last:border-0 dark:border-zinc-900">
                  {row.cells.map((cell, i) => (
                    <td key={`${row.id}:${columns[i]?.label ?? i}`} className="px-4 py-3">
                      {i === 0 ? (
                        <Link
                          href={`/admin/${encodeURIComponent(slug)}/${encodeURIComponent(row.id)}`}
                          className="font-medium hover:underline"
                        >
                          {cell}
                        </Link>
                      ) : (
                        cell
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {data && data.pager.last > 1 && (
        <div className="mt-4 flex items-center gap-3 text-sm">
          <button
            type="button"
            disabled={data.pager.page <= 1}
            onClick={() => page(data.pager.page - 1)}
            className="disabled:opacity-40"
          >
            Previous
          </button>
          <span className="text-zinc-500">
            Page {data.pager.page} of {data.pager.last} · {data.total} records
          </span>
          <button
            type="button"
            disabled={data.pager.page >= data.pager.last}
            onClick={() => page(data.pager.page + 1)}
            className="disabled:opacity-40"
          >
            Next
          </button>
        </div>
      )}
    </AdminLayout>
  );
}
