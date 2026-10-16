<script setup lang="ts">
import { useForm } from "@inertiajs/vue3";

import type { Post } from "../types";

const props = defineProps<{ post?: Post }>();

const input =
  "mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700";

const form = useForm({ title: props.post?.title ?? "", body: props.post?.body ?? "" });

function submit(): void {
  if (props.post) {
    form.put(`/posts/${props.post.id}`);
  } else {
    form.post("/posts");
  }
}
</script>

<template>
  <form class="space-y-4" @submit.prevent="submit">
    <label class="block">
      <span class="font-medium">Title</span>
      <input v-model="form.title" :class="input" required />
      <p v-if="form.errors.title" class="mt-1 text-sm text-red-600">{{ form.errors.title }}</p>
    </label>
    <label class="block">
      <span class="font-medium">Body</span>
      <textarea v-model="form.body" :class="input" rows="6" />
      <p v-if="form.errors.body" class="mt-1 text-sm text-red-600">{{ form.errors.body }}</p>
    </label>
    <button
      type="submit"
      :disabled="form.processing"
      class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
    >
      {{ post ? "Save" : "Create" }}
    </button>
  </form>
</template>
