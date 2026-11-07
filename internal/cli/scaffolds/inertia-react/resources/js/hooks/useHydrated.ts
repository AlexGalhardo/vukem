import { useEffect, useState } from "react";

// False on the server and on the first client render, true once the app has
// hydrated. With SSR the form is visible and focusable before this runs, and
// hydration resets an uncontrolled input, so a JS-only control should stay
// disabled until it returns true.
export function useHydrated(): boolean {
  const [hydrated, setHydrated] = useState(false);

  useEffect(() => setHydrated(true), []);

  return hydrated;
}
