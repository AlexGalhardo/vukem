import type { Metadata } from "next";
import { ChangelogPage, changelogMetadata } from "@/components/changelog-page";

export const metadata: Metadata = changelogMetadata;

export default function Changelog() {
  return <ChangelogPage />;
}
