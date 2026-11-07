"use client";

import { Tooltip } from "@base-ui/react/tooltip";
import { Toaster } from "@/components/toaster";

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <Tooltip.Provider delay={400}>
      <Toaster>{children}</Toaster>
    </Tooltip.Provider>
  );
}
