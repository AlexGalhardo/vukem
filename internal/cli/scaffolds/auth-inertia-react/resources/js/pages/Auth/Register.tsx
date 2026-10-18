import { Head, Link, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../../components/Field";
import PasswordField from "../../components/PasswordField";
import { checkConfirmation, checkPassword, passwordAdvice } from "../../lib/password-policy";

export default function Register() {
  const form = useForm({ name: "", email: "", password: "", password_confirmation: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/register", { onFinish: () => form.reset("password", "password_confirmation") });
  };

  return (
    <>
      <Head title="Create an account" />
      <h1 className="mb-4 text-2xl font-semibold">Create an account</h1>
      <form onSubmit={submit} className="space-y-4">
        <Field
          label="Name"
          autoComplete="name"
          required
          value={form.data.name}
          onChange={(e) => form.setData("name", e.target.value)}
          error={form.errors.name}
        />
        <Field
          label="Email"
          type="email"
          autoComplete="email"
          required
          value={form.data.email}
          onChange={(e) => form.setData("email", e.target.value)}
          error={form.errors.email}
        />
        <PasswordField
          label="Password"
          name="password"
          autoComplete="new-password"
          value={form.data.password}
          onChange={(value) => form.setData("password", value)}
          error={form.errors.password}
          checks={checkPassword(form.data.password)}
          advice={passwordAdvice(form.data.password)}
        />
        <PasswordField
          label="Confirm password"
          name="password_confirmation"
          autoComplete="new-password"
          value={form.data.password_confirmation}
          onChange={(value) => form.setData("password_confirmation", value)}
          error={form.errors.password_confirmation}
          checks={checkConfirmation(form.data.password, form.data.password_confirmation)}
        />
        <button
          type="submit"
          disabled={form.processing}
          className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          Create account
        </button>
      </form>
      <p className="mt-6 text-sm">
        <Link href="/login" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Already registered?
        </Link>
      </p>
    </>
  );
}
