import { evaluate } from "@mdx-js/mdx";
import * as runtime from "react/jsx-runtime";
import remarkGfm from "remark-gfm";
import rehypeSlug from "rehype-slug";
import remarkCodeBlocks from "@/lib/remark-code-blocks";
import { mdxComponents } from "@/components/mdx";

/** Compiles and renders an MDX body on the server (build time). */
export async function renderMdx(body: string, file: string) {
  try {
    const { default: Content } = await evaluate(body, {
      ...runtime,
      development: false,
      remarkPlugins: [remarkGfm, remarkCodeBlocks],
      rehypePlugins: [rehypeSlug],
    });
    return <Content components={mdxComponents} />;
  } catch (err) {
    throw new Error(`content/docs/${file}.mdx: ${err instanceof Error ? err.message : String(err)}`, { cause: err });
  }
}
