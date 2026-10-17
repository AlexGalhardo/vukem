import { Head, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../../components/Field";

export default function ResetPassword({ token, email }: { token: string; email: string }) {
  const form = useForm({ token, email, password: "", password_confirmation: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/reset-password", { onFinish: () => form.reset("password", "password_confirmation") });
  };

  return (
    <>
      <Head title="Choose a new password" />
      <h1 className="mb-4 text-2xl font-semibold">Choose a new password</h1>
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
        <Field
          label="New password (at least 12 characters)"
          type="password"
          autoComplete="new-password"
          minLength={12}
          required
          value={form.data.password}
          onChange={(e) => form.setData("password", e.target.value)}
          error={form.errors.password}
        />
        <Field
          label="Confirm password"
          type="password"
          autoComplete="new-password"
          required
          value={form.data.password_confirmation}
          onChange={(e) => form.setData("password_confirmation", e.target.value)}
          error={form.errors.password_confirmation}
        />
        <button
          type="submit"
          disabled={form.processing}
          className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          Reset password
        </button>
      </form>
    </>
  );
}
