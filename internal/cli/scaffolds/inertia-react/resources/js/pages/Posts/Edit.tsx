import { Head } from "@inertiajs/react";

import PostForm from "../../components/PostForm";
import type { Post } from "../../types";

export default function Edit({ post }: { post: Post }) {
  return (
    <>
      <Head title={`Edit ${post.title}`} />
      <h1 className="mb-4 text-2xl font-semibold">Edit post</h1>
      <PostForm post={post} />
    </>
  );
}
