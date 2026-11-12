import { Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror the Go mailable's fields (app/mail/finance.go). The defaults are
// Go template actions: `email export` writes them into the HTML and Go escapes
// the real values when the mail is sent.
type Props = { workspace: string; category: string; percent: string; spent: string; limit: string; url: string };

export default function BudgetAlert({ workspace = go(".Workspace"), category = go(".Category"), percent = go(".Percent"), spent = go(".Spent"), limit = go(".Limit"), url = go(".URL") }: Partial<Props>) {
  return (
    <Shell preview="A budget needs attention" title={`${category} is at ${percent}% of its budget`} action="See your budgets" href={url}>
      <Text>{workspace} has spent {spent} of the {limit} monthly budget for {category}.</Text>
    </Shell>
  );
}
