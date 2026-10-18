<script setup lang="ts">
import { Head, Link } from "@inertiajs/vue3";

import { demoInstalled, demoLink } from "../demo";
</script>

<template>
  <Head title="Welcome" />
  <section class="py-12 text-center">
    <h1 class="text-3xl font-semibold tracking-tight">Your Vukem app is running</h1>
    <p class="mt-3 text-zinc-600 dark:text-zinc-400">
      Go on the server, Vue with Inertia in the browser, Tailwind for the styles.
    </p>
    <Link
      v-if="demoInstalled"
      :href="demoLink.href"
      class="mt-6 inline-block rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500"
    >
      See the posts
    </Link>
  </section>
</template>
