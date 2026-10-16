import inertia from "@inertiajs/vite";
import tailwindcss from "@tailwindcss/vite";
import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vite";

import vukem from "./resources/vite/vukem.js";

export default defineConfig({
  plugins: [
    tailwindcss(),
    vue(),
    inertia(),
    vukem({ input: ["resources/css/app.css", "resources/js/app.ts"] }),
  ],
});
