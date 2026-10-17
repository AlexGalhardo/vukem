import { Head, Link, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../../components/Field";

export default function Login() {
  const form = useForm({ email: "", password: "", remember: false });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/login", { onFinish: () => form.reset("password") });
  };

  return (
    <>
      <Head title="Log in" />
      <h1 className="mb-4 text-2xl font-semibold">Log in</h1>
      <form onSubmit={submit} className="space-y-4">
        <Field
          label="Email"
          type="email"
          autoComplete="username"
          required
          value={form.data.email}
          onChange={(e) => form.setData("email", e.target.value)}
          error={form.errors.email}
        />
        <Field
          label="Password"
          type="password"
          autoComplete="current-password"
          required
          value={form.data.password}
          onChange={(e) => form.setData("password", e.target.value)}
          error={form.errors.password}
        />
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={form.data.remember}
            onChange={(e) => form.setData("remember", e.target.checked)}
          />
          Remember me
        </label>
        <button
          type="submit"
          disabled={form.processing}
          className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          Log in
        </button>
      </form>
      <p className="mt-6 flex gap-4 text-sm">
        <Link href="/forgot-password" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Forgot your password?
        </Link>
        <Link href="/register" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Create an account
        </Link>
      </p>
    </>
  );
}
