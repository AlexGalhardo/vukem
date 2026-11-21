import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The build is committed and embedded in the Go binary, so it must be
// deterministic and free of inline code (the CSP forbids it).
export default defineConfig({
  base: "/ui/",
  plugins: [react()],
  build: {
    outDir: "../ui",
    emptyOutDir: true,
    sourcemap: false,
    modulePreload: { polyfill: false },
    cssCodeSplit: false,
    assetsInlineLimit: 0,
  },
});
