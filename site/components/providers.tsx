"use client";

import { ThemeProvider } from "next-themes";
import { Tooltip } from "@base-ui/react/tooltip";
import { Toaster } from "@/components/toaster";

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <Tooltip.Provider delay={400}>
        <Toaster>{children}</Toaster>
      </Tooltip.Provider>
    </ThemeProvider>
  );
}
