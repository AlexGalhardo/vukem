<script setup lang="ts">
import { Head, Link, useForm } from "@inertiajs/vue3";

import Field from "../../components/Field.vue";

defineProps<{
  enabled: boolean;
  pending: boolean;
  secret: string | null;
  qr: string | null;
  recoveryCodes: string[] | null;
  recoveryCodesLeft: number;
}>();

const button = "rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50";

const confirm = useForm({ code: "" });
const regenerate = useForm({ password: "" });
const disable = useForm({ password: "" });

function submitConfirm(): void {
  confirm.post("/user/two-factor/confirm", { onFinish: () => confirm.reset("code") });
}

function submitRegenerate(): void {
  regenerate.post("/user/two-factor/recovery-codes", { onFinish: () => regenerate.reset("password") });
}

function submitDisable(): void {
  disable.post("/user/two-factor/disable", { onFinish: () => disable.reset("password") });
}
</script>

<template>
  <Head title="Two-factor authentication" />
  <h1 class="mb-4 text-2xl font-semibold">Two-factor authentication</h1>
  <div v-if="enabled" class="space-y-4">
    <p>Two-factor authentication is on: logging in asks for a code from your authenticator app.</p>
    <template v-if="recoveryCodes">
      <p>
        <strong>Store these recovery codes somewhere safe.</strong> Each one works once if you lose your device; they
        are not shown again.
      </p>
      <ul class="grid grid-cols-2 gap-2 font-mono">
        <li v-for="code in recoveryCodes" :key="code">{{ code }}</li>
      </ul>
    </template>
    <p v-else>You have {{ recoveryCodesLeft }} recovery codes left.</p>
    <form class="space-y-4" @submit.prevent="submitRegenerate">
      <Field
        v-model="regenerate.password"
        label="Current password"
        type="password"
        autocomplete="current-password"
        required
        :error="regenerate.errors.password"
      />
      <button type="submit" :disabled="regenerate.processing" :class="button">Regenerate recovery codes</button>
    </form>
    <h2 class="pt-4 text-xl font-semibold">Turn off</h2>
    <form class="space-y-4" @submit.prevent="submitDisable">
      <Field
        v-model="disable.password"
        label="Current password"
        type="password"
        autocomplete="current-password"
        required
        :error="disable.errors.password"
      />
      <button type="submit" :disabled="disable.processing" :class="button">Disable two-factor authentication</button>
    </form>
  </div>
  <div v-else-if="pending && qr" class="space-y-4">
    <p>Scan this QR code with your authenticator app, or enter the setup key by hand, then type the code it shows.</p>
    <img class="w-fit" :src="qr" width="200" height="200" alt="Two-factor setup QR code" />
    <p>
      Setup key: <code class="font-mono">{{ secret }}</code>
    </p>
    <form class="space-y-4" @submit.prevent="submitConfirm">
      <Field
        v-model="confirm.code"
        label="Code"
        inputmode="numeric"
        autocomplete="one-time-code"
        required
        :error="confirm.errors.code"
      />
      <button type="submit" :disabled="confirm.processing" :class="button">Confirm</button>
    </form>
  </div>
  <div v-else class="space-y-4">
    <p>Add a second step to your login: a code from an authenticator app on your phone.</p>
    <Link href="/user/two-factor" method="post" as="button" :class="button">Enable two-factor authentication</Link>
  </div>
  <p class="mt-6 text-sm">
    <Link href="/dashboard" class="text-indigo-600 hover:underline dark:text-indigo-400">Back to the dashboard</Link>
  </p>
</template>
