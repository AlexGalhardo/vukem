import type { ReactNode, SelectHTMLAttributes } from "react";

export const primary =
  "rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50";
export const secondary =
  "rounded-md border border-zinc-300 px-4 py-2 font-medium hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-900";
export const danger = "text-sm text-red-600 hover:underline";
export const input =
  "mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700";

export function Card({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800">
      {title && <h2 className="mb-3 font-semibold">{title}</h2>}
      {children}
    </section>
  );
}

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label: string;
  error?: string;
}

// Select is a labelled select with its validation error.
export function Select({ label, error, children, ...rest }: SelectProps) {
  return (
    <label className="block">
      <span className="font-medium">{label}</span>
      <select {...rest} className={input}>
        {children}
      </select>
      {error && <p className="mt-1 text-sm text-red-600">{error}</p>}
    </label>
  );
}
