import type { ChangeEvent, ReactElement } from "react";
import type { Bootstrap } from "../api/types";
import { Link, navigate } from "../lib/history";
import { hrefFor } from "../lib/router";
import type { Route } from "../lib/router";
import { KeyboardIcon } from "./icons";
import { ModeBadge } from "./ModeBadge";
import { ThemeToggle } from "./ThemeToggle";

function currentSource(route: Route): string {
  return route.name === "source" || route.name === "table" ? route.source : "";
}

/** TopBar: the product name, the source switcher, the mode badge and the toggles. */
export function TopBar({ bootstrap, route }: { bootstrap: Bootstrap; route: Route }): ReactElement {
  const onSource = (e: ChangeEvent<HTMLSelectElement>): void => {
    const source = e.target.value;
    navigate(hrefFor(source === "" ? { name: "overview" } : { name: "source", source }));
  };

  return (
    <header className="topbar">
      <Link route={{ name: "overview" }} className="brand">
        Vukem Studio
      </Link>

      <label className="switcher">
        <span className="visually-hidden">Source</span>
        <select value={currentSource(route)} onChange={onSource}>
          <option value="">All sources</option>
          {bootstrap.sources.map((s) => (
            <option key={s.name} value={s.name}>
              {s.name} ({s.kind})
            </option>
          ))}
        </select>
      </label>

      <div className="topbar-end">
        <ModeBadge write={bootstrap.write} />
        <button
          type="button"
          className="icon-button"
          aria-disabled="true"
          title="Keyboard shortcuts arrive with the grid"
          aria-label="Keyboard help (not available yet)"
        >
          <KeyboardIcon />
        </button>
        <ThemeToggle />
        <span className="version muted">{bootstrap.version}</span>
      </div>
    </header>
  );
}
