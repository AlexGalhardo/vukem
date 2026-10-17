import type { Metadata } from "next";
import { SiteShell, siteMetadata, siteViewport } from "@/components/site-shell";

export const metadata: Metadata = {
  ...siteMetadata,
  description: "A produtividade do Laravel e do AdonisJS em Go idiomático.",
  openGraph: { ...siteMetadata.openGraph, locale: "pt_BR" },
};
export const viewport = siteViewport;

export default function PortugueseLayout({ children }: { children: React.ReactNode }) {
  return <SiteShell locale="pt-br">{children}</SiteShell>;
}
