import { useEffect, useId, useMemo, useState } from "react";
import type { FormEvent, ReactElement, RefObject } from "react";
import type { Column } from "../api/types";
import { CloseIcon, PlusIcon } from "../components/icons";
import {
  canFilter,
  columnKind,
  needsValue,
  operatorLabels,
  operatorsFor,
  validateFilter,
} from "./filters";
import type { Filter, Operator, ViewState } from "./filters";

export interface FilterRequest {
  column: string;
  /** Changes on every request, so asking twice for the same column still moves the focus. */
  nonce: number;
}

interface FilterBarProps {
  columns: readonly Column[];
  view: ViewState;
  onChange: (view: ViewState) => void;
  /** The value field, so a shortcut can focus it. */
  valueRef: RefObject<HTMLInputElement | null>;
  request: FilterRequest | null;
}

function describe(f: Filter): string {
  return `${f.column} ${operatorLabels[f.op]}${needsValue(f.op) ? ` "${f.value}"` : ""}`;
}

/**
 * FilterBar edits the filters of the view. All of them must match (AND). The
 * operators offered follow the type of the chosen column; the values are sent
 * as typed and the server checks them as it does for every other page.
 */
export function FilterBar({ columns, view, onChange, valueRef, request }: FilterBarProps): ReactElement {
  const usable = useMemo(() => columns.filter((c) => canFilter(c.name)), [columns]);
  const [column, setColumn] = useState(usable[0]?.name ?? "");
  const [op, setOp] = useState<Operator>("contains");
  const [value, setValue] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const ids = { column: useId(), op: useId(), value: useId(), problem: useId() };

  const kind = columnKind(columns.find((c) => c.name === column)?.type ?? "text");
  const operators = operatorsFor(kind);
  const chosen: Operator = operators.includes(op) ? op : (operators[0] ?? "eq");

  useEffect(() => {
    if (request && usable.some((c) => c.name === request.column)) {
      setColumn(request.column);
      valueRef.current?.focus();
    }
  }, [request, usable, valueRef]);

  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    const filter: Filter = { column, op: chosen, value: needsValue(chosen) ? value : "" };
    const error = validateFilter(filter, columns);
    if (error) {
      setProblem(error);
      return;
    }
    setProblem(null);
    setValue("");
    onChange({ ...view, filters: [...view.filters, filter] });
  };

  return (
    <div className="filter-bar">
      <form className="filter-form" onSubmit={submit} aria-label="Add a filter">
        <label className="field-inline" htmlFor="filter-column">
          <span>Column</span>
          <select id="filter-column" value={column} onChange={(e) => setColumn(e.target.value)}>
            {usable.map((c) => (
              <option key={c.name} value={c.name}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <label className="field-inline" htmlFor={ids.op}>
          <span>Condition</span>
          <select id={ids.op} value={chosen} onChange={(e) => setOp(e.target.value as Operator)}>
            {operators.map((o) => (
              <option key={o} value={o}>
                {operatorLabels[o]}
              </option>
            ))}
          </select>
        </label>
        {needsValue(chosen) ? (
          <label className="field-inline" htmlFor={ids.value}>
            <span>Value</span>
            <input
              id={ids.value}
              ref={valueRef}
              type="text"
              value={value}
              autoComplete="off"
              spellCheck={false}
              aria-describedby={problem ? ids.problem : undefined}
              aria-invalid={problem ? true : undefined}
              onChange={(e) => setValue(e.target.value)}
            />
          </label>
        ) : null}
        <button type="submit" className="button button-small button-with-icon" disabled={usable.length === 0}>
          <PlusIcon />
          Add filter
        </button>
      </form>
      {problem ? (
        <p id={ids.problem} className="filter-problem" role="alert">
          {problem}
        </p>
      ) : null}
      {view.filters.length > 0 ? (
        <ul className="filter-chips" aria-label="Active filters. Every row must match all of them.">
          {view.filters.map((f, i) => (
            <li key={`${i}:${describe(f)}`} className="chip">
              <span className="chip-text">{describe(f)}</span>
              <button
                type="button"
                className="chip-remove"
                aria-label={`Remove filter ${describe(f)}`}
                onClick={() => onChange({ ...view, filters: view.filters.filter((_, k) => k !== i) })}
              >
                <CloseIcon />
              </button>
            </li>
          ))}
          <li>
            <button type="button" className="button button-small" onClick={() => onChange({ ...view, filters: [] })}>
              Clear filters
            </button>
          </li>
        </ul>
      ) : null}
    </div>
  );
}
