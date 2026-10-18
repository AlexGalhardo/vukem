import { Head, Link, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../../components/Field";

interface Props {
  enabled: boolean;
  pending: boolean;
  secret: string | null;
  qr: string | null;
  recoveryCodes: string[] | null;
  recoveryCodesLeft: number;
}

const button =
  "rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50";

export default function TwoFactor({ enabled, pending, secret, qr, recoveryCodes, recoveryCodesLeft }: Props) {
  const confirm = useForm({ code: "" });
  const regenerate = useForm({ password: "" });
  const disable = useForm({ password: "" });

  const submitConfirm = (e: FormEvent) => {
    e.preventDefault();
    confirm.post("/user/two-factor/confirm", { onFinish: () => confirm.reset("code") });
  };

  const submitRegenerate = (e: FormEvent) => {
    e.preventDefault();
    regenerate.post("/user/two-factor/recovery-codes", { onFinish: () => regenerate.reset("password") });
  };

  const submitDisable = (e: FormEvent) => {
    e.preventDefault();
    disable.post("/user/two-factor/disable", { onFinish: () => disable.reset("password") });
  };

  return (
    <>
      <Head title="Two-factor authentication" />
      <h1 className="mb-4 text-2xl font-semibold">Two-factor authentication</h1>
      {enabled ? (
        <div className="space-y-4">
          <p>Two-factor authentication is on: logging in asks for a code from your authenticator app.</p>
          {recoveryCodes ? (
            <>
              <p>
                <strong>Store these recovery codes somewhere safe.</strong> Each one works once if you lose your
                device; they are not shown again.
              </p>
              <ul className="grid grid-cols-2 gap-2 font-mono">
                {recoveryCodes.map((code) => (
                  <li key={code}>{code}</li>
                ))}
              </ul>
            </>
          ) : (
            <p>You have {recoveryCodesLeft} recovery codes left.</p>
          )}
          <form onSubmit={submitRegenerate} className="space-y-4">
            <Field
              label="Current password"
              type="password"
              autoComplete="current-password"
              required
              value={regenerate.data.password}
              onChange={(e) => regenerate.setData("password", e.target.value)}
              error={regenerate.errors.password}
            />
            <button type="submit" disabled={regenerate.processing} className={button}>
              Regenerate recovery codes
            </button>
          </form>
          <h2 className="pt-4 text-xl font-semibold">Turn off</h2>
          <form onSubmit={submitDisable} className="space-y-4">
            <Field
              label="Current password"
              type="password"
              autoComplete="current-password"
              required
              value={disable.data.password}
              onChange={(e) => disable.setData("password", e.target.value)}
              error={disable.errors.password}
            />
            <button type="submit" disabled={disable.processing} className={button}>
              Disable two-factor authentication
            </button>
          </form>
        </div>
      ) : pending && qr ? (
        <div className="space-y-4">
          <p>Scan this QR code with your authenticator app, or enter the setup key by hand, then type the code it shows.</p>
          <img className="w-fit" src={qr} width={200} height={200} alt="Two-factor setup QR code" />
          <p>
            Setup key: <code className="font-mono">{secret}</code>
          </p>
          <form onSubmit={submitConfirm} className="space-y-4">
            <Field
              label="Code"
              inputMode="numeric"
              autoComplete="one-time-code"
              required
              value={confirm.data.code}
              onChange={(e) => confirm.setData("code", e.target.value)}
              error={confirm.errors.code}
            />
            <button type="submit" disabled={confirm.processing} className={button}>
              Confirm
            </button>
          </form>
        </div>
      ) : (
        <div className="space-y-4">
          <p>Add a second step to your login: a code from an authenticator app on your phone.</p>
          <Link href="/user/two-factor" method="post" as="button" className={button}>
            Enable two-factor authentication
          </Link>
        </div>
      )}
      <p className="mt-6 text-sm">
        <Link href="/dashboard" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Back to the dashboard
        </Link>
      </p>
    </>
  );
}
