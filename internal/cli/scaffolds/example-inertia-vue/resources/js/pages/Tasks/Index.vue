<script setup lang="ts">
import { Head, Link, useForm } from "@inertiajs/vue3";

import Field from "../../components/Field.vue";
import type { TasksIndexProps } from "../../page-props";

defineProps<TasksIndexProps>();

const form = useForm({ title: "" });

function submit(): void {
  form.post("/tasks", { onSuccess: () => form.reset() });
}
</script>

<template>
  <Head title="Tasks" />
  <h1 class="mb-4 text-2xl font-semibold">Tasks</h1>
  <form class="mb-6 space-y-2" @submit.prevent="submit">
    <Field v-model="form.title" label="New task" :error="form.errors.title" required />
    <button
      type="submit"
      :disabled="form.processing"
      class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
    >
      Add
    </button>
  </form>
  <p v-if="tasks.length === 0" class="text-zinc-500">Nothing to do.</p>
  <ul v-else class="divide-y divide-zinc-200 dark:divide-zinc-800">
    <li v-for="task in tasks" :key="task.id" class="flex items-center justify-between py-2">
      <span :class="{ 'line-through': task.done }">{{ task.title }}</span>
      <span class="flex gap-4 text-sm">
        <Link :href="`/tasks/${task.id}/edit`" class="hover:underline">Edit</Link>
        <Link :href="`/tasks/${task.id}`" method="delete" as="button" class="text-red-600 hover:underline">
          Delete
        </Link>
      </span>
    </li>
  </ul>
</template>
