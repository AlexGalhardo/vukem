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
  if (href !== window.location.pathname + window.location.search) {
    window.history.pushState(null, "", href);
    window.dispatchEvent(new Event(changeEvent));
  }
}

/**
 * replaceSearch changes the query string of the current page in place, without
 * a history entry: the view of a table (filters, sort) lives there.
 */
export function replaceSearch(search: string): void {
  if (search !== window.location.search) {
    window.history.replaceState(null, "", window.location.pathname + search);
    window.dispatchEvent(new Event(changeEvent));
  }
}

function searchSnapshot(): string {
  return window.location.search;
}

/** useSearch returns the query string of the current location ("" or starting with "?") and follows it. */
export function useSearch(): string {
  return useSyncExternalStore(subscribe, searchSnapshot);
}

/** useRoute returns the route of the current location and follows it. */
export function useRoute(): Route {
  return matchRoute(useSyncExternalStore(subscribe, snapshot));
}

interface LinkProps {
  route: Route;
  /** A query string to add to the route, with its "?". */
  search?: string;
  className?: string;
  current?: boolean;
  /** For a link inside a widget that has its own tab stop. */
  tabIndex?: number;
  ariaLabel?: string;
  children: ReactNode;
}

/** Link is an anchor that navigates in place and still works with new-tab clicks. */
export function Link({ route, search, className, current, tabIndex, ariaLabel, children }: LinkProps): ReactElement {
  const href = hrefFor(route) + (search ?? "");

  const onClick = (e: MouseEvent<HTMLAnchorElement>): void => {
    if (isPlainLeftClick(e)) {
      e.preventDefault();
      navigate(href);
    }
  };

  return (
    <a
      href={href}
      className={className}
      onClick={onClick}
      aria-current={current ? "page" : undefined}
      tabIndex={tabIndex}
      aria-label={ariaLabel}
    >
      {children}
    </a>
  );
}
