import { Head, Link, router } from "@inertiajs/react";
import { useState } from "react";

import type { Post } from "../../types";

interface Props {
  posts: Post[];
  filters: { q: string };
}

export default function Index({ posts, filters }: Props) {
  const [q, setQ] = useState(filters.q);

  const search = (value: string) => {
    setQ(value);
    router.get("/posts", value ? { q: value } : {}, { preserveState: true, replace: true });
  };

  return (
    <>
      <Head title="Posts" />
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Posts</h1>
        <Link href="/posts/create" className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500">
          New post
        </Link>
      </div>
      <input
        type="search"
        value={q}
        onChange={(e) => search(e.target.value)}
        placeholder="Search posts"
        className="my-4 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 dark:border-zinc-700"
      />
      {posts.length === 0 ? (
        <p className="text-zinc-500">No posts yet.</p>
      ) : (
        <ul className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {posts.map((post) => (
            <li key={post.id} className="flex items-center justify-between py-2">
              <Link href={`/posts/${post.id}`} className="hover:underline">
                {post.title}
              </Link>
              <Link
                href={`/posts/${post.id}`}
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
