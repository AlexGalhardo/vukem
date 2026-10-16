import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  async redirects() {
    return [{ source: "/", destination: "/docs/introduction", permanent: false }];
  },
  // Content is read from the filesystem at build time; make sure it ships with
  // the route handlers and pages that need it.
  outputFileTracingIncludes: {
    "/**": ["./content/**/*"],
  },
};

export default nextConfig;
