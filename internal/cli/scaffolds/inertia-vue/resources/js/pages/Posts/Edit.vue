<script setup lang="ts">
import { Head } from "@inertiajs/vue3";

import PostForm from "../../components/PostForm.vue";
import type { Post } from "../../types";

defineProps<{ post: Post }>();
</script>

<template>
  <Head :title="`Edit ${post.title}`" />
  <h1 class="mb-4 text-2xl font-semibold">Edit post</h1>
  <PostForm :post="post" />
</template>
