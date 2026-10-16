<script lang="ts">
  import { useForm } from "@inertiajs/svelte";

  import Field from "../../components/Field.svelte";

  const form = useForm({ email: "" });

  function submit(e: SubmitEvent): void {
    e.preventDefault();
    form.post("/forgot-password");
  }
</script>

<svelte:head>
  <title>Forgot your password?</title>
</svelte:head>

<h1 class="mb-2 text-2xl font-semibold">Forgot your password?</h1>
<p class="mb-4 text-zinc-600 dark:text-zinc-400">We will email you a link to choose a new one.</p>
<form onsubmit={submit} class="space-y-4">
  <Field
    label="Email"
    type="email"
    autocomplete="email"
    required
    bind:value={form.email}
    error={form.errors.email}
  />
  <button
    type="submit"
    disabled={form.processing}
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
  >
    Email me a reset link
  </button>
</form>
