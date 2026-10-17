import type { InputHTMLAttributes } from "react";

interface Props extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  error?: string;
}

// Field is a labelled input with its validation error.
export default function Field({ label, error, ...input }: Props) {
  return (
    <label className="block">
      <span className="font-medium">{label}</span>
      <input
        {...input}
        className="mt-1 block w-full rounded-md border border-zinc-300 bg-transparent px-3 py-2 focus:border-indigo-500 focus:outline-none dark:border-zinc-700"
      />
      {error && <p className="mt-1 text-sm text-red-600">{error}</p>}
    </label>
  );
}
