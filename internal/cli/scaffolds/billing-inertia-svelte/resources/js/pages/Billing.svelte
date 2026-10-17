<script lang="ts">
  import { Link } from "@inertiajs/svelte";

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

  let { plans, subscription, subscribed, checkout }: Props = $props();

  const button = "rounded-md bg-indigo-600 px-4 py-2 font-medium text-white hover:bg-indigo-500 disabled:opacity-50";
  function period(s: Subscription): string {
    if (!s.currentPeriodEnd) return "";
    return (s.cancelAtPeriodEnd ? ", ends on " : ", renews on ") + s.currentPeriodEnd;
  }

  // Checkout and the portal are Stripe pages: the server answers these
  // visits with a full page redirect (inertia.Location).
</script>

<svelte:head>
  <title>Billing</title>
</svelte:head>

<h1 class="mb-4 text-2xl font-semibold">Billing</h1>
{#if checkout}
  <p class="mb-4">Thanks! Your subscription starts as soon as Stripe confirms the payment.</p>
{/if}
{#if subscription}
  <div class="space-y-4">
    <p>
      Your plan: <strong>{subscription.plan}</strong> ({subscription.status}){period(subscription)}.
    </p>
    <Link href="/billing/portal" method="post" as="button" class={button}>Manage billing</Link>
  </div>
{:else}
  <p>You have no subscription yet.</p>
{/if}
{#if !subscribed}
  <h2 class="pt-6 pb-2 text-xl font-semibold">Plans</h2>
  <ul class="space-y-2">
    {#each plans as plan (plan.name)}
      <li>
        <Link href={`/billing/checkout/${plan.name}`} method="post" as="button" class={button}>
          Subscribe to {plan.label}
        </Link>
      </li>
    {/each}
  </ul>
{/if}
<p class="mt-6">
  <Link href="/dashboard" class="text-indigo-600 hover:underline dark:text-indigo-400">Back to the dashboard</Link>
</p>
