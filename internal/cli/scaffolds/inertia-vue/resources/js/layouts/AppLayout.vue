<script setup lang="ts">
import { Link, usePage } from "@inertiajs/vue3";
import { computed } from "vue";

import { demoInstalled, demoLink } from "../demo";
import type { SharedProps } from "../types";

const link = "text-indigo-600 hover:underline dark:text-indigo-400";

const page = usePage<SharedProps>();
const auth = computed(() => page.props.auth);
const message = computed(() => page.flash?.success ?? page.flash?.status);
</script>

<template>
  <header class="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
    <Link href="/" class="font-bold">{{ page.props.appName }}</Link>
    <nav class="flex items-center gap-4">
      <Link v-if="demoInstalled" :href="demoLink.href" :class="link">{{ demoLink.label }}</Link>
      <template v-if="auth?.user">
        <Link href="/dashboard" :class="link">{{ auth.user.name }}</Link>
        <Link href="/logout" method="post" as="button" :class="link">Log out</Link>
      </template>
      <template v-else-if="auth">
        <Link href="/login" :class="link">Log in</Link>
        <Link href="/register" :class="link">Register</Link>
      </template>
    </nav>
  </header>
  <main class="mx-auto max-w-2xl p-4">
    <p
      v-if="typeof message === 'string'"
      role="status"
      class="mb-4 rounded-md bg-indigo-50 px-4 py-3 text-indigo-900 dark:bg-indigo-950 dark:text-indigo-100"
    >
      {{ message }}
    </p>
    <slot />
  </main>
</template>
