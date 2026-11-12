import { Head, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../../components/Field";
import type { Page } from "../../page-props";

export default function Edit({ task }: Page<"Tasks/Edit">) {
  const form = useForm({ title: task.title, done: task.done });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.put(`/tasks/${task.id}`);
  };

  return (
    <>
      <Head title="Edit task" />
      <h1 className="mb-4 text-2xl font-semibold">Edit task</h1>
      <form onSubmit={submit} className="space-y-4">
        <Field
          label="Title"
          value={form.data.title}
          onChange={(e) => form.setData("title", e.target.value)}
          error={form.errors.title}
          required
        />
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={form.data.done} onChange={(e) => form.setData("done", e.target.checked)} />
          Done
        </label>
        <button
          type="submit"
          disabled={form.processing}
          className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          Save
        </button>
      </form>
    </>
  );
}
