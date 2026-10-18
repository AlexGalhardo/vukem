import { createInertiaApp } from "@inertiajs/react";

import AppLayout from "./layouts/AppLayout";

// Dark mode follows the "dark" class on <html>: the saved choice
// (localStorage.theme = "dark" | "light") or the system preference. A theme
// toggle flips the class and saves it.
try {
  const theme = localStorage.getItem("theme");
  const dark = theme ? theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.classList.toggle("dark", dark);
} catch {
  // Storage can be blocked (private mode): keep the default theme.
}

// The @inertiajs/vite plugin resolves pages from ./pages and mounts the app.
// The nonce lets the progress bar styles through the CSP.
// It mounts on #app, the default inertia.Config.RootID; when you change
// RootID, pass the same value here as `id`.
createInertiaApp({
  layout: () => AppLayout,
  nonce: document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')?.nonce,
  progress: { color: "#4f46e5" },
});
