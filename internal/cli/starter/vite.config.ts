import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

import vukem from "./resources/vite/vukem.js";

export default defineConfig({
  plugins: [
    tailwindcss(),
    vukem({ input: ["resources/css/app.css", "resources/js/app.ts"] }),
  ],
});
