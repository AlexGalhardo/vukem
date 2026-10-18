import { SiteShell, siteMetadata, siteViewport } from "@/components/site-shell";

export const metadata = siteMetadata;
export const viewport = siteViewport;

export default function EnglishLayout({ children }: { children: React.ReactNode }) {
  return <SiteShell>{children}</SiteShell>;
}
