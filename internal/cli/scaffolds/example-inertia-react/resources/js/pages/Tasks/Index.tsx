import { Head, Link, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../../components/Field";
import type { Page } from "../../page-props";

export default function Index({ tasks }: Page<"Tasks/Index">) {
  const form = useForm({ title: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/tasks", { onSuccess: () => form.reset() });
  };

  return (
    <>
      <Head title="Tasks" />
      <h1 className="mb-4 text-2xl font-semibold">Tasks</h1>
      <form onSubmit={submit} className="mb-6 space-y-2">
        <Field
          label="New task"
          value={form.data.title}
          onChange={(e) => form.setData("title", e.target.value)}
          error={form.errors.title}
          required
        />
        <button
          type="submit"
          disabled={form.processing}
          className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          Add
        </button>
      </form>
      {tasks.length === 0 ? (
        <p className="text-zinc-500">Nothing to do.</p>
      ) : (
        <ul className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {tasks.map((task) => (
            <li key={task.id} className="flex items-center justify-between py-2">
              <span className={task.done ? "line-through" : ""}>{task.title}</span>
              <span className="flex gap-4 text-sm">
                <Link href={`/tasks/${task.id}/edit`} className="hover:underline">
                  Edit
                </Link>
                <Link
                  href={`/tasks/${task.id}`}
                  method="delete"
                  as="button"
                  className="text-red-600 hover:underline"
                >
                  Delete
                </Link>
              </span>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}
