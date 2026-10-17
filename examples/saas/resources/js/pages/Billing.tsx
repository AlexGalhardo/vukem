import { Head, Link } from "@inertiajs/react";

interface Plan {
  name: string;
  label: string;
  current: boolean;
}

interface Subscription {
  plan: string;
  status: string;
  valid: boolean;
  currentPeriodEnd: string;
  cancelAtPeriodEnd: boolean;
}

interface Props {
  plans: Plan[];
  subscription: Subscription | null;
  subscribed: boolean;
  checkout: boolean;
}

const button = "rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50";

// Checkout and the portal are Stripe pages: the server answers these
// visits with a full page redirect (inertia.Location).
export default function Billing({ plans, subscription, subscribed, checkout }: Props) {
  return (
    <>
      <Head title="Billing" />
      <h1 className="mb-4 text-2xl font-semibold">Billing</h1>
      {checkout && <p className="mb-4">Thanks! Your subscription starts as soon as Stripe confirms the payment.</p>}
      {subscription ? (
        <div className="space-y-4">
          <p>
            Your plan: <strong>{subscription.plan}</strong> ({subscription.status})
            {subscription.currentPeriodEnd &&
              (subscription.cancelAtPeriodEnd
                ? `, ends on ${subscription.currentPeriodEnd}`
                : `, renews on ${subscription.currentPeriodEnd}`)}
            .
          </p>
          <Link href="/billing/portal" method="post" as="button" className={button}>
            Manage billing
          </Link>
        </div>
      ) : (
        <p>You have no subscription yet.</p>
      )}
      {!subscribed && (
        <>
          <h2 className="pt-6 pb-2 text-xl font-semibold">Plans</h2>
          <ul className="space-y-2">
            {plans.map((plan) => (
              <li key={plan.name}>
                <Link href={`/billing/checkout/${plan.name}`} method="post" as="button" className={button}>
                  Subscribe to {plan.label}
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
      <p className="mt-6">
        <Link href="/dashboard" className="text-indigo-600 hover:underline dark:text-indigo-400">
          Back to the dashboard
        </Link>
      </p>
    </>
  );
}
