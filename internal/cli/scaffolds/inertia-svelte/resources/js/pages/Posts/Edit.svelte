<script lang="ts">
  import PostForm from "../../components/PostForm.svelte";
  import type { Post } from "../../types";

  let { post }: { post: Post } = $props();
</script>

<svelte:head>
  <title>Edit {post.title}</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Edit post</h1>
<PostForm {post} />
