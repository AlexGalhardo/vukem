<script setup lang="ts">
import { Head, Link, usePage } from "@inertiajs/vue3";
import { computed } from "vue";

import type { SharedProps } from "../types";

const page = usePage<SharedProps>();
const user = computed(() => page.props.auth?.user);
</script>

<template>
  <Head title="Dashboard" />
  <h1 class="text-2xl font-semibold">Hello, {{ user?.name }}</h1>
  <p v-if="user?.email_verified_at" class="mt-2">Your email is verified.</p>
  <p v-else class="mt-2">
    <Link href="/email/verify" class="text-indigo-600 hover:underline dark:text-indigo-400">Verify your email</Link>
  </p>
</template>
