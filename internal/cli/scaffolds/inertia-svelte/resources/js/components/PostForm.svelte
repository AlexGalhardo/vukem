<script lang="ts">
  import { useForm } from "@inertiajs/svelte";

  import type { Post } from "../types";

  let { post }: { post?: Post } = $props();

  const form = useForm(() => ({ title: post?.title ?? "", body: post?.body ?? "" }));

  const input =
    "mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700";

  function submit(e: SubmitEvent): void {
    e.preventDefault();

    if (post) {
      form.put(`/posts/${post.id}`);
    } else {
      form.post("/posts");
    }
  }
</script>

<form onsubmit={submit} class="space-y-4">
  <label class="block">
    <span class="font-medium">Title</span>
    <input class={input} bind:value={form.title} required />
    {#if form.errors.title}
      <p class="mt-1 text-sm text-red-600">{form.errors.title}</p>
    {/if}
  </label>
  <label class="block">
    <span class="font-medium">Body</span>
    <textarea class={input} rows={6} bind:value={form.body}></textarea>
    {#if form.errors.body}
      <p class="mt-1 text-sm text-red-600">{form.errors.body}</p>
    {/if}
  </label>
  <button
    type="submit"
    disabled={form.processing}
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
  >
    {post ? "Save" : "Create"}
  </button>
</form>
