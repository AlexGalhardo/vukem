<script setup lang="ts">
import { Head, Link, useForm } from "@inertiajs/vue3";

import Field from "../../components/Field.vue";

const form = useForm({ name: "", email: "", password: "", password_confirmation: "" });

function submit(): void {
  form.post("/register", { onFinish: () => form.reset("password", "password_confirmation") });
}
</script>

<template>
  <Head title="Create an account" />
  <h1 class="mb-4 text-2xl font-semibold">Create an account</h1>
  <form class="space-y-4" @submit.prevent="submit">
    <Field v-model="form.name" label="Name" autocomplete="name" required :error="form.errors.name" />
    <Field v-model="form.email" label="Email" type="email" autocomplete="email" required :error="form.errors.email" />
    <Field
      v-model="form.password"
      label="Password (at least 12 characters)"
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
      Create account
    </button>
  </form>
  <p class="mt-6 text-sm">
    <Link href="/login" class="text-indigo-600 hover:underline dark:text-indigo-400">Already registered?</Link>
  </p>
</template>
