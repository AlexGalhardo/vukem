<script lang="ts">
  import { Link } from "@inertiajs/svelte";

  import type { Post } from "../../types";

  let { post }: { post: Post } = $props();
</script>

<svelte:head>
  <title>{post.title}</title>
</svelte:head>

<article>
  <h1 class="text-2xl font-semibold">{post.title}</h1>
  <p class="mt-4 whitespace-pre-line">{post.body}</p>
</article>
<div class="mt-6 flex gap-4">
  <Link href={`/posts/${post.id}/edit`} class="text-indigo-600 hover:underline dark:text-indigo-400">Edit</Link>
  <Link href="/posts" class="text-indigo-600 hover:underline dark:text-indigo-400">Back to posts</Link>
</div>
