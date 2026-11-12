import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { workspace: string; url: string };

export default function PaymentFailed({ workspace = go(".Workspace"), url = go(".URL") }: Partial<Props>) {
  return (
    <Shell preview="Your payment failed" title="Your payment failed" action="Update payment method" href={url}>
      <Text>We could not charge the card for {workspace}. Update the payment method to keep Pro; Stripe will retry the payment for a few days.</Text>
    </Shell>
  );
}
