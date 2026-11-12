import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { name: string; link: string };

export default function ResetPassword({ name = go(".User.Name"), link = go(".Link") }: Partial<Props>) {
  return (
    <Shell preview="Reset your password" title="Reset your password" action="Choose a new password" href={link}>
      <Text>Hi {name}, we got a request to reset your password. If it was not you, ignore this email: nothing changes.</Text>
    </Shell>
  );
}
