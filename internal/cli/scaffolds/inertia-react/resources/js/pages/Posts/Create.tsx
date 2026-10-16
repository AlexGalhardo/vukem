import { Head } from "@inertiajs/react";

import PostForm from "../../components/PostForm";

export default function Create() {
  return (
    <>
      <Head title="New post" />
      <h1 className="mb-4 text-2xl font-semibold">New post</h1>
      <PostForm />
    </>
  );
}
