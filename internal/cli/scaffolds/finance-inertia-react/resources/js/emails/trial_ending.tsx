import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { workspace: string; url: string };

export default function TrialEnding({ workspace = go(".Workspace"), url = go(".URL") }: Partial<Props>) {
  return (
    <Shell preview="Your Pro trial ends in three days" title="Your Pro trial is ending" action="Manage billing" href={url}>
      <Text>The Pro trial of {workspace} ends in three days. Keep unlimited accounts, budgets and CSV import by adding a payment method.</Text>
    </Shell>
  );
}
