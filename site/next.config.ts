import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  // The root layout lives in a route group, so the 404 is app/global-not-found.tsx.
  experimental: { globalNotFound: true },
  async redirects() {
    return [
      { source: "/", destination: "/docs/introduction", permanent: false },
      // The Portuguese translation was removed; old links land on the English page.
      { source: "/pt-br/:path*", destination: "/:path*", permanent: true },
    ];
  },
  // Content is read from the filesystem at build time; make sure it ships with
  // the route handlers and pages that need it.
  outputFileTracingIncludes: {
    "/**": ["./content/**/*"],
  },
};

export default nextConfig;
