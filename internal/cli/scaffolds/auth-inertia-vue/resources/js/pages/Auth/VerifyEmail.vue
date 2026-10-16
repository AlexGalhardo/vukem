<script setup lang="ts">
import { Head, Link, usePage } from "@inertiajs/vue3";
import { computed } from "vue";

import type { SharedProps } from "../../types";

const page = usePage<SharedProps>();
const email = computed(() => page.props.auth?.user?.email);
</script>

<template>
  <Head title="Verify your email" />
  <h1 class="mb-2 text-2xl font-semibold">Verify your email</h1>
  <p class="mb-4">We sent a link to {{ email }}. Open it to verify your address.</p>
  <Link
    href="/email/verification-notification"
    method="post"
    as="button"
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500"
  >
    Send the link again
  </Link>
</template>
