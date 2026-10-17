import { Head, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../../components/Field";

export default function ForgotPassword() {
  const form = useForm({ email: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/forgot-password");
  };

  return (
    <>
      <Head title="Forgot your password?" />
      <h1 className="mb-2 text-2xl font-semibold">Forgot your password?</h1>
      <p className="mb-4 text-zinc-600 dark:text-zinc-400">We will email you a link to choose a new one.</p>
      <form onSubmit={submit} className="space-y-4">
        <Field
          label="Email"
          type="email"
          autoComplete="email"
          required
          value={form.data.email}
          onChange={(e) => form.setData("email", e.target.value)}
          error={form.errors.email}
        />
        <button
          type="submit"
          disabled={form.processing}
          className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          Email me a reset link
        </button>
      </form>
    </>
  );
}
