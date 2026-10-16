import { Head, Link } from "@inertiajs/react";

import type { Post } from "../../types";

export default function Show({ post }: { post: Post }) {
  return (
    <>
      <Head title={post.title} />
      <article>
        <h1 className="text-2xl font-semibold">{post.title}</h1>
        <p className="mt-4 whitespace-pre-line">{post.body}</p>
      </article>
      <div className="mt-6 flex gap-4">
        <Link href={`/posts/${post.id}/edit`} className="text-indigo-600 hover:underline dark:text-indigo-400">
          Edit
        </Link>
        <Link href="/posts" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Back to posts
        </Link>
      </div>
    </>
  );
}
