<script lang="ts">
  import { Link, usePage } from "@inertiajs/svelte";

  import type { SharedProps } from "../types";

  const page = usePage<SharedProps>();
  const user = $derived(page.props.auth?.user);
</script>

<svelte:head>
  <title>Dashboard</title>
</svelte:head>

<h1 class="text-2xl font-semibold">Hello, {user?.name}</h1>
{#if user?.email_verified_at}
  <p class="mt-2">Your email is verified.</p>
{:else}
  <p class="mt-2">
    <Link href="/email/verify" class="text-indigo-600 hover:underline dark:text-indigo-400">Verify your email</Link>
  </p>
{/if}
