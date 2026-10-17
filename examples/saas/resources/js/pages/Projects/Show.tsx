import { Head, Link } from "@inertiajs/react";

import type { Project } from "../../types";

export default function Show({ project }: { project: Project }) {
  return (
    <>
      <Head title={project.name} />
      <article>
        <h1 className="text-2xl font-semibold">{project.name}</h1>
        <p className="mt-4 whitespace-pre-line">{project.description}</p>
      </article>
      <div className="mt-6 flex gap-4">
        <Link href={`/projects/${project.id}/edit`} className="text-indigo-600 hover:underline dark:text-indigo-400">
          Edit
        </Link>
        <Link href="/projects" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Back to projects
        </Link>
      </div>
    </>
  );
}
