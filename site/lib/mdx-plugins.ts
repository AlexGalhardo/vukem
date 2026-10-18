import type { CompileOptions } from "@mdx-js/mdx";
import remarkGfm from "remark-gfm";
import rehypeSlug from "rehype-slug";
// Relative .ts import so scripts/check-mdx.ts can load this file with plain node.
import remarkCodeBlocks from "./remark-code-blocks.ts";

/** The MDX plugins of every docs page, shared by lib/mdx.tsx and scripts/check-mdx.ts. */
export const remarkPlugins: NonNullable<CompileOptions["remarkPlugins"]> = [remarkGfm, remarkCodeBlocks];
export const rehypePlugins: NonNullable<CompileOptions["rehypePlugins"]> = [rehypeSlug];
