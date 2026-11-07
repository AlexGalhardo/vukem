import type { APIRoute } from "astro";
import { getEveryDoc } from "@/lib/content";
import { site } from "@/lib/site";
import { currentVersion } from "@/lib/versions";

export const prerender = true;

const HOME = "/docs/introduction";

function escapeXml(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

export const GET: APIRoute = () => {
  const urls = [
    ...getEveryDoc().map((doc) => ({
      loc: `${site.url}${doc.href}/`,
      lastmod: doc.updated,
      priority: doc.href === HOME ? "1.0" : doc.version.id === currentVersion.id ? "0.8" : "0.3",
    })),
    { loc: `${site.url}/changelog/`, lastmod: undefined, priority: "0.5" },
  ];

  const body = [
    `<?xml version="1.0" encoding="UTF-8"?>`,
    `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`,
    ...urls.map((url) => {
      const lastmod = url.lastmod ? `<lastmod>${escapeXml(url.lastmod)}</lastmod>` : "";
      return `<url><loc>${escapeXml(url.loc)}</loc>${lastmod}<changefreq>weekly</changefreq><priority>${url.priority}</priority></url>`;
    }),
    `</urlset>`,
  ].join("");

  return new Response(body, { headers: { "Content-Type": "application/xml; charset=utf-8" } });
};
