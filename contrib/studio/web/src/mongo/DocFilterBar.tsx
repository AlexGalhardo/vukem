import { useId, useState } from "react";
import type { FormEvent, ReactElement } from "react";
import { CloseIcon, PlusIcon } from "../components/icons";
import { needsValue, operatorLabels, operators } from "../grid/filters";
import type { Filter, Operator, ViewState } from "../grid/filters";
import { canUseField } from "./docs";

function describe(f: Filter): string {
  return `${f.column} ${operatorLabels[f.op]}${needsValue(f.op) ? ` "${f.value}"` : ""}`;
}

/**
 * DocFilterBar edits the filters of a collection view: a field path, an
 * operator from the fixed table and a scalar. The server builds the MongoDB
 * filter from those three; nothing typed here is ever a MongoDB operator.
 */
export function DocFilterBar({ fields, view, onChange }: { fields: readonly string[]; view: ViewState; onChange: (view: ViewState) => void }): ReactElement {
  const [column, setColumn] = useState("");
  const [op, setOp] = useState<Operator>("eq");
  const [value, setValue] = useState("");
  const [problem, setProblem] = useState<string | null>(null);
  const ids = { column: useId(), op: useId(), value: useId(), list: useId(), problem: useId() };

  const submit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    const name = column.trim();
    if (!canUseField(name)) {
      setProblem("Give a field path such as name or address.city: letters, digits and underscores, joined by dots.");
      return;
    }
    if (needsValue(op) && value === "") {
      setProblem(`Give a value for ${name}.`);
      return;
    }
    setProblem(null);
    setValue("");
    onChange({ ...view, filters: [...view.filters, { column: name, op, value: needsValue(op) ? value : "" }] });
  };

  return (
    <div className="filter-bar">
      <form className="filter-form" onSubmit={submit} aria-label="Add a filter">
        <label className="field-inline" htmlFor={ids.column}>
          <span>Field</span>
          <input
            id={ids.column}
            type="text"
            list={ids.list}
            value={column}
            autoComplete="off"
            spellCheck={false}
            aria-describedby={problem ? ids.problem : undefined}
            aria-invalid={problem ? true : undefined}
            onChange={(e) => setColumn(e.target.value)}
          />
          <datalist id={ids.list}>
            {fields.filter(canUseField).map((f) => (
              <option key={f} value={f} />
            ))}
          </datalist>
        </label>
        <label className="field-inline" htmlFor={ids.op}>
          <span>Condition</span>
          <select id={ids.op} value={op} onChange={(e) => setOp(e.target.value as Operator)}>
            {operators.map((o) => (
              <option key={o} value={o}>
                {operatorLabels[o]}
              </option>
            ))}
          </select>
        </label>
        {needsValue(op) ? (
          <label className="field-inline" htmlFor={ids.value}>
            <span>Value</span>
            <input id={ids.value} type="text" value={value} autoComplete="off" spellCheck={false} onChange={(e) => setValue(e.target.value)} />
          </label>
        ) : null}
        <button type="submit" className="button button-small button-with-icon">
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
        <ul className="filter-chips" aria-label="Active filters. Every document must match all of them.">
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
