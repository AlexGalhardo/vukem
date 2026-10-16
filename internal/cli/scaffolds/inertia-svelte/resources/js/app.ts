import { createInertiaApp } from "@inertiajs/svelte";

import AppLayout from "./layouts/AppLayout.svelte";

// The @inertiajs/vite plugin resolves pages from ./pages and mounts the app.
// The nonce lets the progress bar styles through the CSP.
createInertiaApp({
  layout: () => AppLayout,
  nonce: document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')?.nonce,
  progress: { color: "#4f46e5" },
});
