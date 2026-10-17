import type { Metadata } from "next";
import { ChangelogPage, changelogMetadata } from "@/components/changelog-page";

export const metadata: Metadata = changelogMetadata("pt-br");

export default function Changelog() {
  return <ChangelogPage locale="pt-br" />;
}
