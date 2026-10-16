import { useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import type { Post } from "../types";

const input =
  "mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700";

export default function PostForm({ post }: { post?: Post }) {
  const form = useForm({ title: post?.title ?? "", body: post?.body ?? "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();

    if (post) {
      form.put(`/posts/${post.id}`);
    } else {
      form.post("/posts");
    }
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <label className="block">
        <span className="font-medium">Title</span>
        <input
          className={input}
          value={form.data.title}
          onChange={(e) => form.setData("title", e.target.value)}
          required
        />
        {form.errors.title && <p className="mt-1 text-sm text-red-600">{form.errors.title}</p>}
      </label>
      <label className="block">
        <span className="font-medium">Body</span>
        <textarea
          className={input}
          rows={6}
          value={form.data.body}
          onChange={(e) => form.setData("body", e.target.value)}
        />
        {form.errors.body && <p className="mt-1 text-sm text-red-600">{form.errors.body}</p>}
      </label>
      <button
        type="submit"
        disabled={form.processing}
        className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
      >
        {post ? "Save" : "Create"}
      </button>
    </form>
  );
}
