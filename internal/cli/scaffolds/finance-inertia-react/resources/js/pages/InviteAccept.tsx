import { Head, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import { primary } from "../components/ui";
import type { Page } from "../page-props";

export default function InviteAccept({ token }: Page<"InviteAccept">) {
  const form = useForm({});

  const submit = (e: FormEvent) => {
    e.preventDefault();
    form.post(`/invites/${token}`);
  };

  return (
    <>
      <Head title="Join a workspace" />
      <h1 className="text-2xl font-semibold">Join a workspace</h1>
      <p className="mt-2">Accept the invitation to start working in the shared workspace. It only works for the address it was sent to.</p>
      <form onSubmit={submit} className="mt-4">
        <button type="submit" disabled={form.processing} className={primary}>
          Accept the invitation
        </button>
      </form>
    </>
  );
}
