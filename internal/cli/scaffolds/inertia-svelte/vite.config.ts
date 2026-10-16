import inertia from "@inertiajs/vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

import vukem from "./resources/vite/vukem.js";

export default defineConfig({
  plugins: [
    tailwindcss(),
    svelte(),
    inertia(),
    vukem({ input: ["resources/css/app.css", "resources/js/app.ts"] }),
  ],
});
