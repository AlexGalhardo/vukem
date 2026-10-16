<script lang="ts">
  import { Link, usePage } from "@inertiajs/svelte";

  import type { SharedProps } from "../../types";

  const page = usePage<SharedProps>();
</script>

<svelte:head>
  <title>Verify your email</title>
</svelte:head>

<h1 class="mb-2 text-2xl font-semibold">Verify your email</h1>
<p class="mb-4">We sent a link to {page.props.auth?.user?.email}. Open it to verify your address.</p>
<Link
  href="/email/verification-notification"
  method="post"
  as="button"
  class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500"
>
  Send the link again
</Link>
