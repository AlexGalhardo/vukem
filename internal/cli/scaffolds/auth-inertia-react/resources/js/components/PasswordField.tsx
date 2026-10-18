import { useId, useState } from "react";

import type { PasswordAdvice, PasswordCheck } from "../lib/password-policy";

interface Props {
  label: string;
  // The server's field name ("password", "password_confirmation"): it is what
  // a password manager reads. The DOM id comes from useId, so two fields with
  // the same name on one page still get distinct ids.
  name: string;
  value: string;
  onChange: (value: string) => void;
  autoComplete: "new-password" | "current-password";
  error?: string;
  // The server rules this value must satisfy, from lib/password-policy.
  // Omit them on a login form: it must not advertise the policy.
  checks?: readonly PasswordCheck[];
  // Optional strength rating. Kept visually apart from `checks` because it is
  // advice: the server accepts anything `checks` accepts, whatever this says.
  advice?: PasswordAdvice;
}

const meter = ["bg-zinc-300 dark:bg-zinc-700", "bg-red-500", "bg-amber-500", "bg-lime-500", "bg-green-600"];

// PasswordField is a password input with a show/hide toggle inside the field
// and, when `checks` are given, the server's rules checked as you type.
//
// The label is bound with htmlFor instead of wrapping the input: a <label> that
// encloses the toggle button folds the button's text into the field's
// accessible name, and the field announces as "Password Show password".
//
// Visibility lives in component state only, so a revealed password is hidden
// again on the next mount; revealing it is a deliberate act every time.
//
// The checklist carries role="list" explicitly: Tailwind's reset sets
// list-style: none, which drops the list role in some screen readers. Each
// rule's state is in its text too, never in its colour alone.
export default function PasswordField({
  label,
  name,
  value,
  onChange,
  autoComplete,
  error,
  checks,
  advice,
}: Props) {
  const [visible, setVisible] = useState(false);
  const id = useId();
  const errorId = `${id}-error`;
  const checksId = `${id}-checks`;
  const adviceId = `${id}-advice`;

  const describedBy = [error && errorId, checks && checksId, advice && adviceId].filter(Boolean).join(" ");

  return (
    <div>
      <label htmlFor={id} className="font-medium">
        {label}
      </label>
      <div className="relative mt-1">
        <input
          id={id}
          name={name}
          type={visible ? "text" : "password"}
          autoComplete={autoComplete}
          required
          value={value}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy || undefined}
          className="block w-full rounded-md border border-zinc-300 bg-transparent py-2 pr-11 pl-3 focus:border-indigo-500 focus:outline-none dark:border-zinc-700"
        />
        <button
          type="button"
          onClick={() => setVisible((shown) => !shown)}
          aria-label={visible ? "Hide password" : "Show password"}
          aria-pressed={visible}
          aria-controls={id}
          className="absolute inset-y-0 right-0 flex w-11 items-center justify-center rounded-r-md text-zinc-500 hover:text-zinc-900 focus:outline-2 focus:outline-offset-2 focus:outline-indigo-500 dark:text-zinc-400 dark:hover:text-zinc-100"
        >
          <svg
            aria-hidden="true"
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
            className="size-5"
          >
            <path d="M1.5 10S4.8 4.5 10 4.5 18.5 10 18.5 10 15.2 15.5 10 15.5 1.5 10 1.5 10Z" />
            <circle cx="10" cy="10" r="2.75" />
            {!visible && <path d="M3.5 3.5l13 13" />}
          </svg>
        </button>
      </div>
      {error && (
        <p id={errorId} className="mt-1 text-sm text-red-600">
          {error}
        </p>
      )}
      {checks && (
        <ul id={checksId} role="list" className="mt-2 space-y-1 text-sm">
          {checks.map((check) => (
            <li
              key={check.id}
              className={check.met ? "text-green-700 dark:text-green-400" : "text-zinc-600 dark:text-zinc-400"}
            >
              <span aria-hidden="true" className="mr-1.5 inline-block w-3 text-center">
                {check.met ? "✓" : "•"}
              </span>
              {check.label}
              <span className="sr-only">{check.met ? " (met)" : " (not met yet)"}</span>
            </li>
          ))}
        </ul>
      )}
      {advice && (
        <div id={adviceId} className="mt-2 text-sm text-zinc-600 dark:text-zinc-400">
          <div aria-hidden="true" className="mb-1 flex gap-1">
            {[1, 2, 3, 4].map((step) => (
              <span
                key={step}
                className={`h-1 flex-1 rounded-full ${step <= advice.score ? meter[advice.score] : meter[0]}`}
              />
            ))}
          </div>
          Strength: {advice.label} (advice, not a requirement). {advice.hint}
        </div>
      )}
    </div>
  );
}
