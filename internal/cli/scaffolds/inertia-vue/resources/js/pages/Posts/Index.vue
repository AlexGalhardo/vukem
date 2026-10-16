<script setup lang="ts">
import { Head, Link, router } from "@inertiajs/vue3";
import { ref } from "vue";

import type { Post } from "../../types";

const props = defineProps<{ posts: Post[]; filters: { q: string } }>();

const q = ref(props.filters.q);

function search(value: string): void {
  q.value = value;
  router.get("/posts", value ? { q: value } : {}, { preserveState: true, replace: true });
}
</script>

<template>
  <Head title="Posts" />
  <div class="flex items-center justify-between">
    <h1 class="text-2xl font-semibold">Posts</h1>
    <Link href="/posts/create" class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500">
      New post
    </Link>
  </div>
  <input
    type="search"
    :value="q"
    placeholder="Search posts"
    class="my-4 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 dark:border-zinc-700"
    @input="search(($event.target as HTMLInputElement).value)"
  />
  <p v-if="posts.length === 0" class="text-zinc-500">No posts yet.</p>
  <ul v-else class="divide-y divide-zinc-200 dark:divide-zinc-800">
    <li v-for="post in posts" :key="post.id" class="flex items-center justify-between py-2">
      <Link :href="`/posts/${post.id}`" class="hover:underline">{{ post.title }}</Link>
      <Link :href="`/posts/${post.id}`" method="delete" as="button" class="text-sm text-red-600 hover:underline">
        Delete
      </Link>
    </li>
  </ul>
</template>
