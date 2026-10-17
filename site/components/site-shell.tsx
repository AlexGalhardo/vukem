import type { Metadata, Viewport } from "next";
import { Inter, JetBrains_Mono } from "next/font/google";
import { Providers } from "@/components/providers";
import { Header } from "@/components/header";
import { dict, localeInfo, type Locale } from "@/lib/i18n";
import { site } from "@/lib/site";
import "@/app/globals.css";

const inter = Inter({ subsets: ["latin"], variable: "--font-inter", display: "swap" });
const mono = JetBrains_Mono({ subsets: ["latin"], variable: "--font-mono-face", display: "swap" });

export const siteMetadata: Metadata = {
  metadataBase: new URL(site.url),
  title: { default: `${site.name} — Go web framework`, template: `%s — ${site.name}` },
  description: site.tagline,
  applicationName: site.name,
  openGraph: { siteName: site.name, type: "website" },
  twitter: { card: "summary" },
};

export const siteViewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#ffffff" },
    { media: "(prefers-color-scheme: dark)", color: "#050505" },
  ],
};

/**
 * The <html> document shared by the root layout of each locale
 * (app/(en)/layout.tsx, app/(pt-br)/layout.tsx): one root layout per
 * locale is what gives every page the right <html lang> while staying static.
 */
export function SiteShell({ locale, children }: { locale: Locale; children: React.ReactNode }): React.ReactElement {
  return (
    <html lang={localeInfo[locale].tag} suppressHydrationWarning className={`${inter.variable} ${mono.variable}`}>
      <body className="min-h-dvh font-sans">
        <a
          href="#content"
          className="sr-only z-50 rounded-md bg-bg px-3 py-2 text-sm focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
        >
          {dict(locale).skipToContent}
        </a>
        <Providers>
          <Header locale={locale} />
          {children}
        </Providers>
      </body>
    </html>
  );
}
