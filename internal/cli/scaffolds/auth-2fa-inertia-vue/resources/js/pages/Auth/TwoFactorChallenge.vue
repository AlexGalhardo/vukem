<script setup lang="ts">
import { Head, useForm } from "@inertiajs/vue3";
import { ref } from "vue";

import Field from "../../components/Field.vue";

const recovery = ref(false);
const form = useForm({ code: "", recovery_code: "" });

function submit(): void {
  form.post("/two-factor-challenge", { onFinish: () => form.reset() });
}

// Only one field is sent filled: the server checks the app code first.
function toggle(): void {
  recovery.value = !recovery.value;
  form.reset();
  form.clearErrors();
}
</script>

<template>
  <Head title="Two-factor authentication" />
  <h1 class="mb-4 text-2xl font-semibold">Two-factor authentication</h1>
  <p class="mb-4">{{ recovery ? "Enter one of your recovery codes." : "Enter the code from your authenticator app." }}</p>
  <form class="space-y-4" @submit.prevent="submit">
    <Field
      v-if="recovery"
      v-model="form.recovery_code"
      label="Recovery code"
      autocomplete="off"
      required
      :error="form.errors.recovery_code"
    />
    <Field
      v-else
      v-model="form.code"
      label="Code"
      inputmode="numeric"
      autocomplete="one-time-code"
      autofocus
      required
      :error="form.errors.code"
    />
    <button
      type="submit"
      :disabled="form.processing"
      class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
    >
      Log in
    </button>
  </form>
  <p class="mt-6 text-sm">
    <button type="button" class="text-indigo-600 hover:underline dark:text-indigo-400" @click="toggle">
      {{ recovery ? "Use a code from your authenticator app" : "Lost your device? Use a recovery code" }}
    </button>
  </p>
</template>
