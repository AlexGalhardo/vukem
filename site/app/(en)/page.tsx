import { StaticRedirect } from "@/components/static-redirect";

// The documentation is the site: "/" opens the introduction.
export default function Home() {
  return <StaticRedirect href="/docs/introduction/" />;
}
