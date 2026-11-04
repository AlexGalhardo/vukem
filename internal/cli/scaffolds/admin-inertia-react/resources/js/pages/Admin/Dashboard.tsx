import { Link } from "@inertiajs/react";
import { useEffect, useState } from "react";

import AdminLayout from "../../layouts/AdminLayout";
import { type DashboardData, getJSON } from "../../lib/adminApi";

export default function Dashboard({ dataUrl }: { dataUrl: string }) {
  const [data, setData] = useState<DashboardData | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const ctl = new AbortController();
    getJSON<DashboardData>(dataUrl, ctl.signal)
      .then(setData)
      .catch((e: Error) => e.name !== "AbortError" && setError(e.message));
    return () => ctl.abort();
  }, [dataUrl]);

  return (
    <AdminLayout title="Dashboard">
      {error && (
        <p role="alert" className="text-red-600">
          {error}
        </p>
      )}
      {!data && !error && <p className="text-zinc-500">Loading…</p>}
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {(data?.cards ?? []).map((card) => (
          <Link
            key={card.url}
            href={`/admin/${card.url.replace(`${dataUrl}/`, "")}`}
            className="rounded-lg border border-zinc-200 p-5 hover:bg-zinc-50 dark:border-zinc-800 dark:hover:bg-zinc-900"
          >
            <div className="text-sm text-zinc-500">{card.label}</div>
            <div className="mt-1 text-3xl font-semibold tabular-nums">{card.count}</div>
          </Link>
        ))}
      </div>
    </AdminLayout>
  );
}
