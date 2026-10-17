import type { Metadata } from "next";
import { ChangelogPage, changelogMetadata } from "@/components/changelog-page";

export const metadata: Metadata = changelogMetadata("en");

export default function Changelog() {
  return <ChangelogPage locale="en" />;
}
