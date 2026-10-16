import { Head, useForm } from "@inertiajs/react";
import { useState, type FormEvent } from "react";

import Field from "../../components/Field";

export default function TwoFactorChallenge() {
  const [recovery, setRecovery] = useState(false);
  const form = useForm({ code: "", recovery_code: "" });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post("/two-factor-challenge", { onFinish: () => form.reset() });
  };

  return (
    <>
      <Head title="Two-factor authentication" />
      <h1 className="mb-4 text-2xl font-semibold">Two-factor authentication</h1>
      <p className="mb-4">
        {recovery ? "Enter one of your recovery codes." : "Enter the code from your authenticator app."}
      </p>
      <form onSubmit={submit} className="space-y-4">
        {recovery ? (
          <Field
            label="Recovery code"
            autoComplete="off"
            required
            value={form.data.recovery_code}
            onChange={(e) => form.setData("recovery_code", e.target.value)}
            error={form.errors.recovery_code}
          />
        ) : (
          <Field
            label="Code"
            inputMode="numeric"
            autoComplete="one-time-code"
            autoFocus
            required
            value={form.data.code}
            onChange={(e) => form.setData("code", e.target.value)}
            error={form.errors.code}
          />
        )}
        <button
          type="submit"
          disabled={form.processing}
          className="rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
        >
          Log in
        </button>
      </form>
      <p className="mt-6 text-sm">
        <button
          type="button"
          onClick={() => {
            // Only one field is sent filled: the server checks the app code first.
            setRecovery(!recovery);
            form.reset();
            form.clearErrors();
          }}
          className="text-indigo-600 hover:underline dark:text-indigo-400"
        >
          {recovery ? "Use a code from your authenticator app" : "Lost your device? Use a recovery code"}
        </button>
      </p>
    </>
  );
}
