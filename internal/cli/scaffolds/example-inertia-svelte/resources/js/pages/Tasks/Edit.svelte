<script lang="ts">
  import { useForm } from "@inertiajs/svelte";

  import Field from "../../components/Field.svelte";
  import type { Page } from "../../page-props";

  let { task }: Page<"Tasks/Edit"> = $props();

  const form = useForm(() => ({ title: task.title, done: task.done }));

  function submit(e: SubmitEvent): void {
    e.preventDefault();
    form.put(`/tasks/${task.id}`);
  }
</script>

<svelte:head>
  <title>Edit task</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Edit task</h1>
<form onsubmit={submit} class="space-y-4">
  <Field label="Title" bind:value={form.title} error={form.errors.title} required />
  <label class="flex items-center gap-2">
    <input type="checkbox" bind:checked={form.done} />
    Done
  </label>
  <button
    type="submit"
    disabled={form.processing}
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
  >
    Save
  </button>
</form>
