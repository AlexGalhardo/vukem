import { Head, Link, useForm } from "@inertiajs/react";
import type { FormEvent } from "react";

import Field from "../components/Field";
import { Card, Select, danger, primary } from "../components/ui";
import type { Page } from "../page-props";

export default function Workspace({ workspaceDetails, members, invites, limits, canManage }: Page<"Workspace">) {
  const settings = useForm({ name: workspaceDetails.name, currency: workspaceDetails.currency });
  const invite = useForm({ email: "", role: "member" });
  const create = useForm({ name: "", currency: "USD" });

  const save = (e: FormEvent) => {
    e.preventDefault();
    settings.put("/workspace");
  };

  const send = (e: FormEvent) => {
    e.preventDefault();
    invite.post("/workspace/invites", { onSuccess: () => invite.reset("email") });
  };

  const add = (e: FormEvent) => {
    e.preventDefault();
    create.post("/workspaces", { onSuccess: () => create.reset("name") });
  };

  return (
    <>
      <Head title="Workspace" />
      <h1 className="text-2xl font-semibold">{workspaceDetails.name}</h1>
      <Card title="Settings">
        <form onSubmit={save} className="grid gap-3 sm:grid-cols-3">
          <Field label="Name" required disabled={!canManage} value={settings.data.name} onChange={(e) => settings.setData("name", e.target.value)} error={settings.errors.name} />
          <Field label="Currency (ISO 4217)" required maxLength={3} disabled={!canManage} value={settings.data.currency} onChange={(e) => settings.setData("currency", e.target.value.toUpperCase())} error={settings.errors.currency} />
          {canManage && (
            <div className="self-end">
              <button type="submit" disabled={settings.processing} className={primary}>
                Save
              </button>
            </div>
          )}
        </form>
      </Card>
      <Card title="Members">
        <ul className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {members.map((m) => (
            <li key={m.userId} className="flex items-center justify-between py-2">
              <span>
                {m.name} <span className="text-sm text-zinc-500">{m.email}, {m.role}</span>
              </span>
              {canManage && m.role !== "owner" && (
                <Link href={`/workspace/members/${m.userId}`} method="delete" as="button" className={danger}>
                  Remove
                </Link>
              )}
            </li>
          ))}
          {invites.map((i) => (
            <li key={i.id} className="py-2 text-sm text-zinc-500">
              {i.email} ({i.role}): invitation pending
            </li>
          ))}
        </ul>
        {limits.members >= 0 && (
          <p className="mt-3 text-sm text-zinc-500">
            The {limits.plan} plan allows {limits.members} member.{" "}
            <Link href="/billing" className="text-indigo-600 hover:underline dark:text-indigo-400">
              Upgrade
            </Link>{" "}
            to invite people.
          </p>
        )}
      </Card>
      {canManage && (
        <Card title="Invite someone">
          <form onSubmit={send} className="grid gap-3 sm:grid-cols-3">
            <Field label="Email" type="email" required value={invite.data.email} onChange={(e) => invite.setData("email", e.target.value)} error={invite.errors.email} />
            <Select label="Role" value={invite.data.role} onChange={(e) => invite.setData("role", e.target.value)} error={invite.errors.role}>
              <option value="member">Member: records transactions</option>
              <option value="admin">Admin: also manages data and people</option>
            </Select>
            <div className="self-end">
              <button type="submit" disabled={invite.processing} className={primary}>
                Send invitation
              </button>
            </div>
          </form>
        </Card>
      )}
      <Card title="Create another workspace">
        <form onSubmit={add} className="grid gap-3 sm:grid-cols-3">
          <Field label="Name" required value={create.data.name} onChange={(e) => create.setData("name", e.target.value)} error={create.errors.name} />
          <Field label="Currency (ISO 4217)" required maxLength={3} value={create.data.currency} onChange={(e) => create.setData("currency", e.target.value.toUpperCase())} error={create.errors.currency} />
          <div className="self-end">
            <button type="submit" disabled={create.processing} className={primary}>
              Create
            </button>
          </div>
        </form>
      </Card>
    </>
  );
}
