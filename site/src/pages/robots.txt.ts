import type { APIRoute } from "astro";
import { site } from "@/lib/site";

export const prerender = true;

export const GET: APIRoute = () =>
  new Response(`User-Agent: *\nAllow: /\n\nHost: ${site.url}\nSitemap: ${site.url}/sitemap.xml\n`, {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
  });
