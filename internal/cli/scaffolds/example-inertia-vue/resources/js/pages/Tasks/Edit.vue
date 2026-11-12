<script setup lang="ts">
import { Head, useForm } from "@inertiajs/vue3";

import Field from "../../components/Field.vue";
import type { TasksEditProps } from "../../page-props";

const props = defineProps<TasksEditProps>();

const form = useForm({ title: props.task.title, done: props.task.done });

function submit(): void {
  form.put(`/tasks/${props.task.id}`);
}
</script>

<template>
  <Head title="Edit task" />
  <h1 class="mb-4 text-2xl font-semibold">Edit task</h1>
  <form class="space-y-4" @submit.prevent="submit">
    <Field v-model="form.title" label="Title" :error="form.errors.title" required />
    <label class="flex items-center gap-2">
      <input v-model="form.done" type="checkbox" />
      Done
    </label>
    <button
      type="submit"
      :disabled="form.processing"
      class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
    >
      Save
    </button>
  </form>
</template>
