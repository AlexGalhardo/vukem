import { useState } from "react";
import type { ReactElement } from "react";
import { shown } from "./docs";
import type { Json } from "./json";

const maxChildren = 200;
const wrapperKeys = ["$oid", "$date", "$numberLong", "$numberInt", "$numberDouble", "$numberDecimal", "$uuid"];

function isWrapper(v: Json): boolean {
  if (v === null || typeof v !== "object" || Array.isArray(v)) return false;
  const keys = Object.keys(v);
  return keys.length === 1 && wrapperKeys.includes(keys[0] as string);
}

function isContainer(v: Json): v is Json[] | { [key: string]: Json } {
  return v !== null && typeof v === "object" && !isWrapper(v);
}

function entries(v: Json[] | { [key: string]: Json }): [string, Json][] {
  return Array.isArray(v) ? v.map((x, i) => [String(i), x]) : Object.entries(v);
}

function summary(v: Json[] | { [key: string]: Json }): string {
  const n = Array.isArray(v) ? v.length : Object.keys(v).length;
  return Array.isArray(v) ? `[${n}]` : `{${n}}`;
}

function Leaf({ value }: { value: Json }): ReactElement {
  if (value === null) return <span className="tree-null">null</span>;
  if (typeof value === "string") return <span className="tree-string">"{value}"</span>;
  if (typeof value === "number" || typeof value === "boolean") return <span className="tree-number">{String(value)}</span>;
  return <span className="tree-wrapper">{shown(value)}</span>;
}

function Node({ name, value }: { name: string; value: Json }): ReactElement {
  const [open, setOpen] = useState(false);

  if (!isContainer(value)) {
    return (
      <li className="tree-row">
        <span className="tree-key mono">{name}</span>: <Leaf value={value} />
      </li>
    );
  }

  const children = entries(value);
  return (
    <li className="tree-row">
      <button type="button" className="tree-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span aria-hidden="true" className="tree-caret">
          {open ? "▾" : "▸"}
        </span>
        <span className="tree-key mono">{name}</span> <span className="muted">{summary(value)}</span>
      </button>
      {open ? (
        <ul className="tree-list">
          {children.slice(0, maxChildren).map(([k, v]) => (
            <Node key={k} name={k} value={v} />
          ))}
          {children.length > maxChildren ? <li className="muted">and {children.length - maxChildren} more</li> : null}
        </ul>
      ) : null}
    </li>
  );
}

/** JsonTree shows a document as a collapsible tree: the top level is open, nested values open on demand. */
export function JsonTree({ value, label }: { value: { [key: string]: Json }; label: string }): ReactElement {
  return (
    <ul className="tree-list tree-root" aria-label={label}>
      {Object.entries(value).map(([k, v]) => (
        <Node key={k} name={k} value={v} />
      ))}
    </ul>
  );
}
