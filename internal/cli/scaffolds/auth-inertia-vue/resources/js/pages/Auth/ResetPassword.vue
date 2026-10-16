<script setup lang="ts">
import { Head, useForm } from "@inertiajs/vue3";

import Field from "../../components/Field.vue";

const props = defineProps<{ token: string; email: string }>();

const form = useForm({ token: props.token, email: props.email, password: "", password_confirmation: "" });

function submit(): void {
  form.post("/reset-password", { onFinish: () => form.reset("password", "password_confirmation") });
}
</script>

<template>
  <Head title="Choose a new password" />
  <h1 class="mb-4 text-2xl font-semibold">Choose a new password</h1>
  <form class="space-y-4" @submit.prevent="submit">
    <Field v-model="form.email" label="Email" type="email" autocomplete="email" required :error="form.errors.email" />
    <Field
      v-model="form.password"
      label="New password (at least 12 characters)"
      type="password"
      autocomplete="new-password"
      minlength="12"
      required
      :error="form.errors.password"
    />
    <Field
      v-model="form.password_confirmation"
      label="Confirm password"
      type="password"
      autocomplete="new-password"
      required
      :error="form.errors.password_confirmation"
    />
    <button
      type="submit"
      :disabled="form.processing"
      class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
    >
      Reset password
    </button>
  </form>
</template>
