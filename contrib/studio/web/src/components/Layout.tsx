import type { ReactElement, ReactNode } from "react";
import type { Bootstrap } from "../api/types";
import type { Route } from "../lib/router";
import { SideNav } from "./SideNav";
import { TopBar } from "./TopBar";

/** Layout is the shell: skip link, top bar, source navigation and the main area. */
export function Layout({
  bootstrap,
  route,
  children,
}: {
  bootstrap: Bootstrap;
  route: Route;
  children: ReactNode;
}): ReactElement {
  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <TopBar bootstrap={bootstrap} route={route} />
      <SideNav sources={bootstrap.sources} route={route} />
      <main id="main" className="main" tabIndex={-1}>
        {children}
      </main>
    </div>
  );
}
