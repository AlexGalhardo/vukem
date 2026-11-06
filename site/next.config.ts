import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  // GitHub Pages serves the exported out/ directory. There is no Next server:
  // "/" and "/docs" redirect in their pages, and old /pt-br links in the 404 view.
  output: "export",
  // The export writes path.html and a path/ directory (RSC payload) for every
  // route. GitHub Pages prefers the directory and 404s when it has no
  // index.html, so the page has to be path/index.html.
  trailingSlash: true,
  // The root layout lives in a route group, so the 404 is app/global-not-found.tsx.
  experimental: { globalNotFound: true },
  // Content is read from the filesystem at build time; make sure it ships with
  // the route handlers and pages that need it.
  outputFileTracingIncludes: {
    "/**": ["./content/**/*"],
  },
};

export default nextConfig;
