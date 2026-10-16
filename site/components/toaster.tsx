"use client";

import { Toast } from "@base-ui/react/toast";
import { Check } from "lucide-react";

/** Shared manager so any client component can show a toast. */
export const toastManager = Toast.createToastManager();

function ToastList() {
  const { toasts } = Toast.useToastManager();
  return toasts.map((toast) => (
    <Toast.Root
      key={toast.id}
      toast={toast}
      className="absolute right-0 bottom-0 flex w-full items-center gap-2.5 rounded-xl border border-border bg-bg px-4 py-3 text-sm text-fg shadow-lg shadow-black/5 transition-all duration-300 data-ending-style:translate-y-2 data-ending-style:opacity-0 data-starting-style:translate-y-2 data-starting-style:opacity-0 dark:shadow-black/40"
    >
      <Check aria-hidden className="size-4 shrink-0 text-accent" />
      <Toast.Title className="font-medium" />
    </Toast.Root>
  ));
}

export function Toaster({ children }: { children: React.ReactNode }) {
  return (
    <Toast.Provider toastManager={toastManager} timeout={2000} limit={1}>
      {children}
      <Toast.Portal>
        <Toast.Viewport className="fixed right-4 bottom-4 z-[100] w-[min(20rem,calc(100vw-2rem))]">
          <ToastList />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}
