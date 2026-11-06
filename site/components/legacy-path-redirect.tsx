"use client";

import { useEffect } from "react";

// GitHub Pages cannot apply a wildcard redirect. Old /pt-br URLs 404, and this
// sends them to the English path that replaced the removed translation.
export function LegacyPathRedirect(): null {
  useEffect(() => {
    const path = window.location.pathname;
    if (path !== "/pt-br" && !path.startsWith("/pt-br/")) return;
    const next = path.slice("/pt-br".length) || "/";
    window.location.replace(next + window.location.search + window.location.hash);
  }, []);
  return null;
}
