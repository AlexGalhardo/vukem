import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { name: string; link: string };

export default function VerifyEmail({ name = go(".User.Name"), link = go(".Link") }: Partial<Props>) {
  return (
    <Shell preview="Confirm your email address" title="Confirm your email address" action="Verify email" href={link}>
      <Text>Hi {name}, confirm this address to finish creating your account. The link works for one hour.</Text>
    </Shell>
  );
}
