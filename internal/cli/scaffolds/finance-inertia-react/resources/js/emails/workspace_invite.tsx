import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { inviter: string; workspace: string; role: string; url: string };

export default function WorkspaceInvite({ inviter = go(".Inviter"), workspace = go(".Workspace"), role = go(".Role"), url = go(".URL") }: Partial<Props>) {
  return (
    <Shell preview="You were invited to a workspace" title={`Join ${workspace}`} action="Accept the invitation" href={url}>
      <Text>{inviter} invited you to {workspace} as {role}. Sign in with this email address to accept; the link works for seven days.</Text>
    </Shell>
  );
}
