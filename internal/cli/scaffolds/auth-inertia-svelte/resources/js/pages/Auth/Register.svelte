<script lang="ts">
  import { Link, useForm } from "@inertiajs/svelte";

  import Field from "../../components/Field.svelte";

  const form = useForm({ name: "", email: "", password: "", password_confirmation: "" });

  function submit(e: SubmitEvent): void {
    e.preventDefault();
    form.post("/register", { onFinish: () => form.reset("password", "password_confirmation") });
  }
</script>

<svelte:head>
  <title>Create an account</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Create an account</h1>
<form onsubmit={submit} class="space-y-4">
  <Field label="Name" autocomplete="name" required bind:value={form.name} error={form.errors.name} />
  <Field
    label="Email"
    type="email"
    autocomplete="email"
    required
    bind:value={form.email}
    error={form.errors.email}
  />
  <Field
    label="Password (at least 12 characters)"
    type="password"
    autocomplete="new-password"
    minlength={12}
    required
    bind:value={form.password}
    error={form.errors.password}
  />
  <Field
    label="Confirm password"
    type="password"
    autocomplete="new-password"
    required
    bind:value={form.password_confirmation}
    error={form.errors.password_confirmation}
  />
  <button
    type="submit"
    disabled={form.processing}
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
  >
    Create account
  </button>
</form>
<p class="mt-6 text-sm">
  <Link href="/login" class="text-indigo-600 hover:underline dark:text-indigo-400">Already registered?</Link>
</p>
