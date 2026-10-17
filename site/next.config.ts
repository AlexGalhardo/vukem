import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  // English and Portuguese have separate root layouts (for <html lang>), so the 404 is app/global-not-found.tsx.
  experimental: { globalNotFound: true },
  async redirects() {
    return [
      { source: "/", destination: "/docs/introduction", permanent: false },
      { source: "/pt-br", destination: "/pt-br/docs/introduction", permanent: false },
    ];
  },
  // Content is read from the filesystem at build time; make sure it ships with
  // the route handlers and pages that need it.
  outputFileTracingIncludes: {
    "/**": ["./content/**/*"],
  },
};

export default nextConfig;
