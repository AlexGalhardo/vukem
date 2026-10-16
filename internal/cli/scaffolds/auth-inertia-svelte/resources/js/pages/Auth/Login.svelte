<script lang="ts">
  import { Link, useForm } from "@inertiajs/svelte";

  import Field from "../../components/Field.svelte";

  const form = useForm({ email: "", password: "", remember: false });

  function submit(e: SubmitEvent): void {
    e.preventDefault();
    form.post("/login", { onFinish: () => form.reset("password") });
  }
</script>

<svelte:head>
  <title>Log in</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Log in</h1>
<form onsubmit={submit} class="space-y-4">
  <Field
    label="Email"
    type="email"
    autocomplete="username"
    required
    bind:value={form.email}
    error={form.errors.email}
  />
  <Field
    label="Password"
    type="password"
    autocomplete="current-password"
    required
    bind:value={form.password}
    error={form.errors.password}
  />
  <label class="flex items-center gap-2">
    <input type="checkbox" bind:checked={form.remember} />
    Remember me
  </label>
  <button
    type="submit"
    disabled={form.processing}
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
  >
    Log in
  </button>
</form>
<p class="mt-6 flex gap-4 text-sm">
  <Link href="/forgot-password" class="text-indigo-600 hover:underline dark:text-indigo-400">
    Forgot your password?
  </Link>
  <Link href="/register" class="text-indigo-600 hover:underline dark:text-indigo-400">Create an account</Link>
</p>
