import { Head, Link, useForm } from "@inertiajs/react";

import { Card, primary, secondary } from "../components/ui";
import type { Page } from "../page-props";

export default function Billing({ limits, subscription, trialDays, canManage, configured, checkout }: Page<"Billing">) {
  const form = useForm({});
  const pro = limits.plan === "pro";

  return (
    <>
      <Head title="Billing" />
      <h1 className="text-2xl font-semibold">Billing</h1>
      {checkout && (
        <p role="status" className="rounded-md bg-indigo-50 px-4 py-3 text-indigo-900 dark:bg-indigo-950 dark:text-indigo-100">
          Thanks. Pro starts as soon as Stripe confirms the payment method: refresh in a moment.
        </p>
      )}
      {!configured && <p className="text-sm text-amber-700">Stripe is not configured here: set STRIPE_KEY and STRIPE_PRICE_PRO in .env.</p>}
      <div className="grid gap-4 sm:grid-cols-2">
        <Card title="Free">
          <ul className="list-disc space-y-1 pl-5 text-sm">
            <li>1 account</li>
            <li>100 transactions per month</li>
            <li>1 member</li>
          </ul>
        </Card>
        <Card title="Pro">
          <ul className="list-disc space-y-1 pl-5 text-sm">
            <li>Unlimited accounts, transactions and members</li>
            <li>CSV import with column mapping</li>
            <li>Monthly budgets with email alerts</li>
          </ul>
          {pro ? (
            <p className="mt-3 text-sm font-medium text-emerald-700">This workspace is on Pro.</p>
          ) : canManage ? (
            <button className={`${primary} mt-3`} disabled={form.processing || !configured} onClick={() => form.post("/billing/checkout/pro")}>
              {subscription ? "Resubscribe to Pro" : `Start the ${trialDays}-day trial`}
            </button>
          ) : (
            <p className="mt-3 text-sm text-zinc-500">Only the owner can change the plan.</p>
          )}
        </Card>
      </div>
      {subscription && (
        <Card title="Subscription">
          <p>
            Status: <strong>{subscription.status}</strong>
            {subscription.currentPeriodEnd && `, current period ends ${subscription.currentPeriodEnd}`}
            {subscription.cancelAtPeriodEnd && " (cancels at the end of the period)"}
          </p>
          {!subscription.valid && (
            <p className="mt-2 text-sm text-amber-700">
              The subscription is not active, so the workspace is on Free. Update the payment method in the portal.
            </p>
          )}
          {canManage && (
            <button className={`${secondary} mt-3`} disabled={form.processing} onClick={() => form.post("/billing/portal")}>
              Open the billing portal
            </button>
          )}
        </Card>
      )}
      <p className="text-sm text-zinc-500">
        Limits reached? <Link href="/workspace" className="text-indigo-600 hover:underline dark:text-indigo-400">Manage members</Link>.
      </p>
    </>
  );
}
