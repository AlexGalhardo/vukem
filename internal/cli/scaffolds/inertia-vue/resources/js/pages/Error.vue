<script setup lang="ts">
import { Head, Link } from "@inertiajs/vue3";

// Rendered by the server for errors on Inertia visits (404, 403, 500…).
// message is safe to show: 5xx only ever carry the status text.
defineProps<{ status: number; message: string }>();
</script>

<template>
  <Head :title="`${status} ${message}`" />
  <section class="py-12 text-center">
    <p class="text-sm font-semibold text-indigo-600 dark:text-indigo-400">{{ status }}</p>
    <h1 class="mt-2 text-3xl font-semibold tracking-tight">{{ message }}</h1>
    <Link href="/" class="mt-6 inline-block text-indigo-600 hover:underline dark:text-indigo-400">
      Back to the home page
    </Link>
  </section>
</template>
