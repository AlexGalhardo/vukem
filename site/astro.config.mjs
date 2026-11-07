import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@astrojs/react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "astro/config";

const root = path.dirname(fileURLToPath(import.meta.url));

// Static HTML for GitHub Pages. "/" and "/docs" open the introduction.
// Old snapshot roots open that snapshot's introduction.
export default defineConfig({
  site: "https://vukem.dev",
  output: "static",
  outDir: "out",
  trailingSlash: "always",
  build: { format: "directory" },
  server: { port: 3000 },
  integrations: [react()],
  vite: {
    plugins: [tailwindcss()],
    resolve: {
      alias: { "@": root },
    },
  },
  redirects: {
    "/": "/docs/introduction/",
    "/docs": "/docs/introduction/",
    "/docs/0.20": "/docs/0.20/introduction/",
  },
});
