<script setup lang="ts">
import { Head, useForm } from "@inertiajs/vue3";

import Field from "../../components/Field.vue";

const form = useForm({ email: "" });

function submit(): void {
  form.post("/forgot-password");
}
</script>

<template>
  <Head title="Forgot your password?" />
  <h1 class="mb-2 text-2xl font-semibold">Forgot your password?</h1>
  <p class="mb-4 text-zinc-600 dark:text-zinc-400">We will email you a link to choose a new one.</p>
  <form class="space-y-4" @submit.prevent="submit">
    <Field v-model="form.email" label="Email" type="email" autocomplete="email" required :error="form.errors.email" />
    <button
      type="submit"
      :disabled="form.processing"
      class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
    >
      Email me a reset link
    </button>
  </form>
</template>
