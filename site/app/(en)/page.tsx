import { redirect } from "next/navigation";

// The documentation is the site: "/" opens the introduction.
export default function Home() {
  redirect("/docs/introduction");
}
