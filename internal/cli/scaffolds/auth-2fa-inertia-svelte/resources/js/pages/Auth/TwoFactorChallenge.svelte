<script lang="ts">
  import { useForm } from "@inertiajs/svelte";

  import Field from "../../components/Field.svelte";

  let recovery = $state(false);
  const form = useForm({ code: "", recovery_code: "" });

  function submit(e: SubmitEvent): void {
    e.preventDefault();
    form.post("/two-factor-challenge", { onFinish: () => form.reset() });
  }

  // Only one field is sent filled: the server checks the app code first.
  function toggle(): void {
    recovery = !recovery;
    form.reset();
    form.clearErrors();
  }
</script>

<svelte:head>
  <title>Two-factor authentication</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Two-factor authentication</h1>
<p class="mb-4">{recovery ? "Enter one of your recovery codes." : "Enter the code from your authenticator app."}</p>
<form onsubmit={submit} class="space-y-4">
  {#if recovery}
    <Field
      label="Recovery code"
      autocomplete="off"
      required
      bind:value={form.recovery_code}
      error={form.errors.recovery_code}
    />
  {:else}
    <Field
      label="Code"
      inputmode="numeric"
      autocomplete="one-time-code"
      required
      bind:value={form.code}
      error={form.errors.code}
    />
  {/if}
  <button
    type="submit"
    disabled={form.processing}
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
  >
    Log in
  </button>
</form>
<p class="mt-6 text-sm">
  <button type="button" onclick={toggle} class="text-indigo-600 hover:underline dark:text-indigo-400">
    {recovery ? "Use a code from your authenticator app" : "Lost your device? Use a recovery code"}
  </button>
</p>
