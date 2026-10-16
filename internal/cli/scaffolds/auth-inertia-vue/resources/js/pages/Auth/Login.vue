<script setup lang="ts">
import { Head, Link, useForm } from "@inertiajs/vue3";

import Field from "../../components/Field.vue";

const form = useForm({ email: "", password: "", remember: false });

function submit(): void {
  form.post("/login", { onFinish: () => form.reset("password") });
}
</script>

<template>
  <Head title="Log in" />
  <h1 class="mb-4 text-2xl font-semibold">Log in</h1>
  <form class="space-y-4" @submit.prevent="submit">
    <Field
      v-model="form.email"
      label="Email"
      type="email"
      autocomplete="username"
      required
      :error="form.errors.email"
    />
    <Field
      v-model="form.password"
      label="Password"
      type="password"
      autocomplete="current-password"
      required
      :error="form.errors.password"
    />
    <label class="flex items-center gap-2">
      <input v-model="form.remember" type="checkbox" />
      Remember me
    </label>
    <button
      type="submit"
      :disabled="form.processing"
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
</template>
