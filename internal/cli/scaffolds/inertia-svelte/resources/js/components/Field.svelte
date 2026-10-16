<script lang="ts">
  import type { HTMLInputAttributes } from "svelte/elements";

  interface Props extends HTMLInputAttributes {
    label: string;
    error?: string;
  }

  // Field is a labelled input with its validation error; bind:value it.
  let { label, error, value = $bindable(), ...input }: Props = $props();
</script>

<label class="block">
  <span class="font-medium">{label}</span>
  <input
    {...input}
    bind:value
    class="mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700"
  />
  {#if error}
    <p class="mt-1 text-sm text-red-600">{error}</p>
  {/if}
</label>
