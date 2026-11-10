import type { ComponentProps } from "react";
import { withTrailingSlash } from "@/lib/trailing-slash";

/** A plain anchor. The docs site is static HTML, so there is no client router; internal paths get
 * the trailing slash the pages are served at, so a click never goes through a redirect. */
export function Link({ href, ...rest }: ComponentProps<"a">) {
  return <a href={href === undefined ? href : withTrailingSlash(href)} {...rest} />;
}
