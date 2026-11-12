import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { name: string; workspace: string; url: string };

export default function Welcome({ name = go(".Name"), workspace = go(".Workspace"), url = go(".URL") }: Partial<Props>) {
  return (
    <Shell preview="Your workspace is ready" title={`Welcome, ${name}`} action="Open your dashboard" href={url}>
      <Text>Your workspace {workspace} is ready. Add an account, record your first transaction and see where the month goes.</Text>
    </Shell>
  );
}
