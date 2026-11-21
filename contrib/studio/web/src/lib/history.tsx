import { useSyncExternalStore } from "react";
import type { MouseEvent, ReactElement, ReactNode } from "react";
import { hrefFor, isPlainLeftClick, matchRoute } from "./router";
import type { Route } from "./router";

const changeEvent = "studio:navigate";

function subscribe(onChange: () => void): () => void {
  window.addEventListener("popstate", onChange);
  window.addEventListener(changeEvent, onChange);
  return () => {
    window.removeEventListener("popstate", onChange);
    window.removeEventListener(changeEvent, onChange);
  };
}

function snapshot(): string {
  return window.location.pathname;
}

/** navigate moves to an in-app path with the history API, without a reload. */
export function navigate(href: string): void {
  if (href !== window.location.pathname) {
    window.history.pushState(null, "", href);
    window.dispatchEvent(new Event(changeEvent));
  }
}

/** useRoute returns the route of the current location and follows it. */
export function useRoute(): Route {
  return matchRoute(useSyncExternalStore(subscribe, snapshot));
}

interface LinkProps {
  route: Route;
  className?: string;
  current?: boolean;
  children: ReactNode;
}

/** Link is an anchor that navigates in place and still works with new-tab clicks. */
export function Link({ route, className, current, children }: LinkProps): ReactElement {
  const href = hrefFor(route);

  const onClick = (e: MouseEvent<HTMLAnchorElement>): void => {
    if (isPlainLeftClick(e)) {
      e.preventDefault();
      navigate(href);
    }
  };

  return (
    <a href={href} className={className} onClick={onClick} aria-current={current ? "page" : undefined}>
      {children}
    </a>
  );
}
