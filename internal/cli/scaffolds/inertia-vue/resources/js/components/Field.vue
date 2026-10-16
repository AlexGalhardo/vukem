<script setup lang="ts">
// Field is a labelled input with its validation error. Attributes other
// than label and error (type, required, autocomplete...) go to the input.
defineOptions({ inheritAttrs: false });
defineProps<{ label: string; error?: string }>();

const model = defineModel<string>({ required: true });
</script>

<template>
  <label class="block">
    <span class="font-medium">{{ label }}</span>
    <input
      v-bind="$attrs"
      v-model="model"
      class="mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700"
    />
    <p v-if="error" class="mt-1 text-sm text-red-600">{{ error }}</p>
  </label>
</template>
