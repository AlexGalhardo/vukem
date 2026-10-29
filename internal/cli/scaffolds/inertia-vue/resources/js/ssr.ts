import { createInertiaApp } from "@inertiajs/vue3";

import AppLayout from "./layouts/AppLayout.vue";

// Server entry for `vukem build -ssr` (vite build --ssr). Keep the call at the
// top level, not exported: @inertiajs/vite rewrites it into the SSR server
// that `vukem ssr:start` runs. No browser globals (document, localStorage).
createInertiaApp({
  layout: () => AppLayout,
});
