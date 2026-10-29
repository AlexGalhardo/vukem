import { createInertiaApp } from "@inertiajs/react";

import AppLayout from "./layouts/AppLayout";

// Server entry for `vukem build -ssr` (vite build --ssr). Keep the call at the
// top level, not exported: @inertiajs/vite rewrites it into the SSR server
// that `vukem ssr:start` runs. No browser globals (document, localStorage).
createInertiaApp({
  layout: () => AppLayout,
});
