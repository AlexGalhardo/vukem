import type { Metadata } from "next";
import { NotFoundView } from "@/components/not-found-view";
import { SiteShell, siteMetadata } from "@/components/site-shell";

// Each locale has its own root layout, so unmatched URLs need a global 404 (experimental.globalNotFound).
export const metadata: Metadata = { ...siteMetadata, title: "Page not found" };

export default function GlobalNotFound() {
  return (
    <SiteShell locale="en">
      <NotFoundView />
    </SiteShell>
  );
}
