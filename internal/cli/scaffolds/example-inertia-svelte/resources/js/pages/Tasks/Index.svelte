<script lang="ts">
  import { Link, useForm } from "@inertiajs/svelte";

  import Field from "../../components/Field.svelte";
  import type { Page } from "../../page-props";

  let { tasks }: Page<"Tasks/Index"> = $props();

  const form = useForm(() => ({ title: "" }));

  function submit(e: SubmitEvent): void {
    e.preventDefault();
    form.post("/tasks", { onSuccess: () => form.reset() });
  }
</script>

<svelte:head>
  <title>Tasks</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Tasks</h1>
<form onsubmit={submit} class="mb-6 space-y-2">
  <Field label="New task" bind:value={form.title} error={form.errors.title} required />
  <button
    type="submit"
    disabled={form.processing}
    class="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
  >
    Add
  </button>
</form>
{#if tasks.length === 0}
  <p class="text-zinc-500">Nothing to do.</p>
{:else}
  <ul class="divide-y divide-zinc-200 dark:divide-zinc-800">
    {#each tasks as task (task.id)}
      <li class="flex items-center justify-between py-2">
        <span class={task.done ? "line-through" : ""}>{task.title}</span>
        <span class="flex gap-4 text-sm">
          <Link href={`/tasks/${task.id}/edit`} class="hover:underline">Edit</Link>
          <Link href={`/tasks/${task.id}`} method="delete" as="button" class="text-red-600 hover:underline">
            Delete
          </Link>
        </span>
      </li>
    {/each}
  </ul>
{/if}
