import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { workspace: string; amount: string; url: string };

export default function Receipt({
  workspace = go(".Workspace"),
  amount = go(".Amount"),
  url = go(".URL"),
}: Partial<Props>) {
  return (
    <Shell
      preview="Your receipt"
      title={`Receipt for ${amount}`}
      action="View the invoice"
      href={url}
    >
      <Text>Thanks. We charged {amount} for the Pro plan of {workspace}.</Text>
    </Shell>
  );
}
