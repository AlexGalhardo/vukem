import { useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import type { Project } from "../types";

const input =
  "mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700";

export default function ProjectForm({ project }: { project?: Project }) {
  const form = useForm({ name: project?.name ?? "", description: project?.description ?? "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();

    if (project) {
      form.put(`/projects/${project.id}`);
    } else {
      form.post("/projects");
    }
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <label className="block">
        <span className="font-medium">Name</span>
        <input
          className={input}
          value={form.data.name}
          onChange={(e) => form.setData("name", e.target.value)}
          required
        />
        {form.errors.name && <p className="mt-1 text-sm text-red-600">{form.errors.name}</p>}
      </label>
      <label className="block">
        <span className="font-medium">Description</span>
        <textarea
          className={input}
          rows={4}
          value={form.data.description}
          onChange={(e) => form.setData("description", e.target.value)}
        />
        {form.errors.description && <p className="mt-1 text-sm text-red-600">{form.errors.description}</p>}
      </label>
      <button
        type="submit"
        disabled={form.processing}
        className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
      >
        {project ? "Save" : "Create"}
      </button>
    </form>
  );
}
