import type { MetadataRoute } from "next";
import { getEveryDoc } from "@/lib/content";
import { site } from "@/lib/site";
import { currentVersion } from "@/lib/versions";

export const dynamic = "force-static";

// "/" and "/docs" only redirect to the introduction, so the introduction is
// the entry the crawler should see.
const HOME = "/docs/introduction";

export default function sitemap(): MetadataRoute.Sitemap {
  const docs = getEveryDoc().map((doc) => ({
    url: `${site.url}${doc.href}`,
    lastModified: doc.updated,
    changeFrequency: "weekly" as const,
    priority: doc.href === HOME ? 1 : doc.version.id === currentVersion.id ? 0.8 : 0.3,
  }));

  return [
    ...docs,
    {
      url: `${site.url}/changelog`,
      changeFrequency: "weekly" as const,
      priority: 0.5,
    },
  ];
}
