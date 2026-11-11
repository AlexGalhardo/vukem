import type { APIRoute } from "astro";
import { getAllDocs } from "@/lib/content";
import { mdxToMarkdown } from "@/lib/text";
import { site } from "@/lib/site";

export const prerender = true;

export function getStaticPaths() {
  return getAllDocs().map((doc) => ({
    params: { slug: doc.slug.join("/") },
    props: { href: doc.href },
  }));
}

export const GET: APIRoute = ({ props }) => {
  const doc = getAllDocs().find((d) => d.href === props.href);
  if (!doc) return new Response("not found", { status: 404 });

  const body = [`# ${doc.title}`, "", `> ${doc.description}`, "", `Source: ${site.url}${doc.href}/`, "", mdxToMarkdown(doc.body), ""].join("\n");

  return new Response(body, { headers: { "Content-Type": "text/markdown; charset=utf-8" } });
};
