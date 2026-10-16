<script setup lang="ts">
import { Head } from "@inertiajs/vue3";

import PostForm from "../../components/PostForm.vue";
</script>

<template>
  <Head title="New post" />
  <h1 class="mb-4 text-2xl font-semibold">New post</h1>
  <PostForm />
</template>
