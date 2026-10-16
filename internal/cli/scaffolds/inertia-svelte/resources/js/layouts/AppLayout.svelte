<script lang="ts">
  import { Link, usePage } from "@inertiajs/svelte";
  import type { Snippet } from "svelte";

  import type { SharedProps } from "../types";

  let { children }: { children: Snippet } = $props();

  const page = usePage<SharedProps>();
  const auth = $derived(page.props.auth);
  const message = $derived(page.flash?.success ?? page.flash?.status);

  const link = "text-indigo-600 hover:underline dark:text-indigo-400";
</script>

<header class="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
  <Link href="/" class="font-bold">{page.props.appName}</Link>
  <nav class="flex items-center gap-4">
    <Link href="/posts" class={link}>Posts</Link>
    {#if auth?.user}
      <Link href="/dashboard" class={link}>{auth.user.name}</Link>
      <Link href="/logout" method="post" as="button" class={link}>Log out</Link>
    {:else if auth}
      <Link href="/login" class={link}>Log in</Link>
      <Link href="/register" class={link}>Register</Link>
    {/if}
  </nav>
</header>
<main class="mx-auto max-w-2xl p-4">
  {#if typeof message === "string"}
    <p
      role="status"
      class="mb-4 rounded-md bg-indigo-50 px-4 py-3 text-indigo-900 dark:bg-indigo-950 dark:text-indigo-100"
    >
      {message}
    </p>
  {/if}
  {@render children()}
</main>
