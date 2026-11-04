import { Link } from "@inertiajs/react";
import { useEffect, useState } from "react";

import AdminLayout from "../../layouts/AdminLayout";
import { getJSON, type RecordData } from "../../lib/adminApi";

export default function ResourceShow({ slug, id, dataUrl }: { slug: string; id: string; dataUrl: string }) {
  const [data, setData] = useState<RecordData | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const ctl = new AbortController();
    getJSON<RecordData>(`${dataUrl}/${encodeURIComponent(slug)}/${encodeURIComponent(id)}`, ctl.signal)
      .then(setData)
      .catch((e: Error) => e.name !== "AbortError" && setError(e.message));
    return () => ctl.abort();
  }, [dataUrl, slug, id]);

  return (
    <AdminLayout title={data ? `${data.resource.singular} ${data.id}` : id}>
      <Link href={`/admin/${encodeURIComponent(slug)}`} className="mb-4 inline-block text-sm text-zinc-500 hover:underline">
        ← Back
      </Link>
      {error && (
        <p role="alert" className="text-red-600">
          {error}
        </p>
      )}
      {!data && !error && <p className="text-zinc-500">Loading…</p>}
      {data && (
        <dl className="grid max-w-3xl grid-cols-[12rem_1fr] gap-x-6 gap-y-3 text-sm">
          {(data.fields ?? []).map((f) => (
            <div key={f.label} className="contents">
              <dt className="text-zinc-500">{f.label}</dt>
              <dd className="break-words">{f.value}</dd>
            </div>
          ))}
        </dl>
      )}
    </AdminLayout>
  );
}
