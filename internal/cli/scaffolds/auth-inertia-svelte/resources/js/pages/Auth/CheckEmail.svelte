<script lang="ts">
  import { Link } from "@inertiajs/svelte";
</script>

<svelte:head>
  <title>Check your email</title>
</svelte:head>

<h1 class="mb-2 text-2xl font-semibold">Check your email</h1>
<p class="mb-4">
  If the address can be used, we sent it a link to verify it. If it already has an account, we sent a link to sign in or
  reset the password instead.
</p>
<Link href="/login" class="text-indigo-600 hover:underline dark:text-indigo-400">Go to the login page</Link>
