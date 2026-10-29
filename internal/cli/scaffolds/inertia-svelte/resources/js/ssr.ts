import { createInertiaApp } from "@inertiajs/svelte";

import AppLayout from "./layouts/AppLayout.svelte";

// Server entry for `vukem build -ssr` (vite build --ssr). Keep the call at the
// top level, not exported: @inertiajs/vite rewrites it into the SSR server
// that `vukem ssr:start` runs. No browser globals (document, localStorage).
createInertiaApp({
  layout: () => AppLayout,
});
