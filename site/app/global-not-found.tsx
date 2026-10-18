import type { Metadata } from "next";
import { NotFoundView } from "@/components/not-found-view";
import { SiteShell, siteMetadata } from "@/components/site-shell";

// The root layout lives in the (en) route group, so unmatched URLs need a global 404 (experimental.globalNotFound).
export const metadata: Metadata = { ...siteMetadata, title: "Page not found" };

export default function GlobalNotFound() {
  return (
    <SiteShell>
      <NotFoundView />
    </SiteShell>
  );
}
