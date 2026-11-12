import { Section, Text } from "@react-email/components";

import { go } from "./go";
import { Shell } from "./Layout";

// Props mirror app/mail.MonthlySummary. The category rows are a Go range: the
// actions around them are plain text, so the loop survives the export.
type Props = { workspace: string; month: string; income: string; expense: string; net: string; url: string };

export default function MonthlySummary({
  workspace = go(".Workspace"),
  month = go(".Month"),
  income = go(".Income"),
  expense = go(".Expense"),
  net = go(".Net"),
  url = go(".URL"),
}: Partial<Props>) {
  return (
    <Shell preview="How the month went" title={`${workspace}: ${month}`} action="Open the dashboard" href={url}>
      <Text>Income {income}, expenses {expense}, net {net}.</Text>
      <Section>
        <Text style={{ fontWeight: "bold" }}>Where the money went</Text>
        {go("range .Top")}
        <Text style={{ margin: "4px 0" }}>
          {go(".Category")}: {go(".Amount")}
        </Text>
        {go("end")}
      </Section>
    </Shell>
  );
}
