<script lang="ts">
  import { Link, router } from "@inertiajs/svelte";
  import { untrack } from "svelte";

  import type { Post } from "../../types";

  interface Props {
    posts: Post[];
    filters: { q: string };
  }

  let { posts, filters }: Props = $props();

  // Seeded once: the input owns the query after that (preserveState keeps it).
  let q = $state(untrack(() => filters.q));

  function search(): void {
    router.get("/posts", q ? { q } : {}, { preserveState: true, replace: true });
  }
</script>

<svelte:head>
  <title>Posts</title>
</svelte:head>

<div class="flex items-center justify-between">
  <h1 class="text-2xl font-semibold">Posts</h1>
  <Link href="/posts/create" class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500">
    New post
  </Link>
</div>
<input
  type="search"
  bind:value={q}
  oninput={search}
  placeholder="Search posts"
  class="my-4 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 dark:border-zinc-700"
/>
{#if posts.length === 0}
  <p class="text-zinc-500">No posts yet.</p>
{:else}
  <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
    {#each posts as post (post.id)}
      <li class="flex items-center justify-between py-2">
        <Link href={`/posts/${post.id}`} class="hover:underline">{post.title}</Link>
        <Link href={`/posts/${post.id}`} method="delete" as="button" class="text-sm text-red-600 hover:underline">
          Delete
        </Link>
      </li>
    {/each}
  </ul>
{/if}
