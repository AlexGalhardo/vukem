"use client";

import { useEffect } from "react";

/**
 * Where an old /pt-br URL goes, or null when it is not one or would leave
 * this origin. The remainder is rebuilt as a same-origin URL: "/pt-br//evil.com"
 * would otherwise become the protocol-relative "//evil.com".
 */
export function legacyTarget(location: Pick<Location, "origin" | "pathname" | "search" | "hash">): string | null {
  const path = location.pathname;
  if (path !== "/pt-br" && !path.startsWith("/pt-br/")) return null;
  const rest = path.slice("/pt-br".length).replace(/^[/\\]+/, "");
  const url = new URL("/" + rest, location.origin);
  if (url.origin !== location.origin) return null;
  return url.pathname + location.search + location.hash;
}

// GitHub Pages cannot apply a wildcard redirect. Old /pt-br URLs 404, and this
// sends them to the English path that replaced the removed translation.
export function LegacyPathRedirect(): null {
  useEffect(() => {
    const target = legacyTarget(window.location);
    if (target !== null) window.location.replace(target);
  }, []);
  return null;
}
