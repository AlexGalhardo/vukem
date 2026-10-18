<script lang="ts">
  import { Link, useForm } from "@inertiajs/svelte";

  import Field from "../../components/Field.svelte";

  interface Props {
    enabled: boolean;
    pending: boolean;
    secret: string | null;
    qr: string | null;
    recoveryCodes: string[] | null;
    recoveryCodesLeft: number;
  }

  let { enabled, pending, secret, qr, recoveryCodes, recoveryCodesLeft }: Props = $props();

  const button =
    "rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50";

  const confirm = useForm({ code: "" });
  const regenerate = useForm({ password: "" });
  const disable = useForm({ password: "" });

  function submitConfirm(e: SubmitEvent): void {
    e.preventDefault();
    confirm.post("/user/two-factor/confirm", { onFinish: () => confirm.reset("code") });
  }

  function submitRegenerate(e: SubmitEvent): void {
    e.preventDefault();
    regenerate.post("/user/two-factor/recovery-codes", { onFinish: () => regenerate.reset("password") });
  }

  function submitDisable(e: SubmitEvent): void {
    e.preventDefault();
    disable.post("/user/two-factor/disable", { onFinish: () => disable.reset("password") });
  }
</script>

<svelte:head>
  <title>Two-factor authentication</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Two-factor authentication</h1>
{#if enabled}
  <div class="space-y-4">
    <p>Two-factor authentication is on: logging in asks for a code from your authenticator app.</p>
    {#if recoveryCodes}
      <p>
        <strong>Store these recovery codes somewhere safe.</strong> Each one works once if you lose your device; they
        are not shown again.
      </p>
      <ul class="grid grid-cols-2 gap-2 font-mono">
        {#each recoveryCodes as code (code)}
          <li>{code}</li>
        {/each}
      </ul>
    {:else}
      <p>You have {recoveryCodesLeft} recovery codes left.</p>
    {/if}
    <form onsubmit={submitRegenerate} class="space-y-4">
      <Field
        label="Current password"
        type="password"
        autocomplete="current-password"
        required
        bind:value={regenerate.password}
        error={regenerate.errors.password}
      />
      <button type="submit" disabled={regenerate.processing} class={button}>Regenerate recovery codes</button>
    </form>
    <h2 class="pt-4 text-xl font-semibold">Turn off</h2>
    <form onsubmit={submitDisable} class="space-y-4">
      <Field
        label="Current password"
        type="password"
        autocomplete="current-password"
        required
        bind:value={disable.password}
        error={disable.errors.password}
      />
      <button type="submit" disabled={disable.processing} class={button}>Disable two-factor authentication</button>
    </form>
  </div>
{:else if pending && qr}
  <div class="space-y-4">
    <p>Scan this QR code with your authenticator app, or enter the setup key by hand, then type the code it shows.</p>
    <img class="w-fit" src={qr} width="200" height="200" alt="Two-factor setup QR code" />
    <p>
      Setup key: <code class="font-mono">{secret}</code>
    </p>
    <form onsubmit={submitConfirm} class="space-y-4">
      <Field
        label="Code"
        inputmode="numeric"
        autocomplete="one-time-code"
        required
        bind:value={confirm.code}
        error={confirm.errors.code}
      />
      <button type="submit" disabled={confirm.processing} class={button}>Confirm</button>
    </form>
  </div>
{:else}
  <div class="space-y-4">
    <p>Add a second step to your login: a code from an authenticator app on your phone.</p>
    <Link href="/user/two-factor" method="post" as="button" class={button}>Enable two-factor authentication</Link>
  </div>
{/if}
<p class="mt-6 text-sm">
  <Link href="/dashboard" class="text-indigo-600 hover:underline dark:text-indigo-400">Back to the dashboard</Link>
</p>
