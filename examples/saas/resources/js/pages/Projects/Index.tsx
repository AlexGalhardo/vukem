import { Head, Link, router } from "@inertiajs/react";
import { useState } from "react";

import type { Project } from "../../types";

interface Props {
  projects: Project[];
  filters: { q: string };
  freeLimit: number;
}

export default function Index({ projects, filters, freeLimit }: Props) {
  const [q, setQ] = useState(filters.q);

  const search = (value: string) => {
    setQ(value);
    router.get("/projects", value ? { q: value } : {}, { preserveState: true, replace: true });
  };

  return (
    <>
      <Head title="Projects" />
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Projects</h1>
        <Link href="/projects/create" className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500">
          New project
        </Link>
      </div>
      <p className="mt-2 text-sm text-zinc-500">
        Free accounts have up to {freeLimit} projects; a plan removes the limit.
      </p>
      <input
        type="search"
        value={q}
        onChange={(e) => search(e.target.value)}
        placeholder="Search projects"
        className="my-4 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 dark:border-zinc-700"
      />
      {projects.length === 0 ? (
        <p className="text-zinc-500">No projects yet.</p>
      ) : (
        <ul className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {projects.map((project) => (
            <li key={project.id} className="flex items-center justify-between py-2">
              <Link href={`/projects/${project.id}`} className="hover:underline">
                {project.name}
              </Link>
              <Link
                href={`/projects/${project.id}`}
                method="delete"
                as="button"
                className="text-sm text-red-600 hover:underline"
              >
                Delete
              </Link>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
