"use client";

import { useState } from "react";
import { Dialog } from "@base-ui/react/dialog";
import { Menu, X } from "lucide-react";
import { Sidebar } from "@/components/sidebar";
import type { NavGroup } from "@/lib/content";

/** Drawer with the sidebar for small screens. */
export function MobileNav({ groups }: { groups: NavGroup[] }) {
  const [open, setOpen] = useState(false);

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger
        aria-label="Open navigation"
        className="inline-flex size-8 items-center justify-center rounded-lg text-muted hover:bg-hover hover:text-fg lg:hidden"
      >
        <Menu aria-hidden className="size-4.5" />
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/30 transition-opacity data-ending-style:opacity-0 data-starting-style:opacity-0 dark:bg-black/60" />
        <Dialog.Popup className="fixed inset-y-0 left-0 z-50 flex w-[min(20rem,85vw)] flex-col border-r border-border bg-bg transition-transform duration-200 data-ending-style:-translate-x-full data-starting-style:-translate-x-full">
          <div className="flex h-14 items-center justify-between border-b border-border px-4">
            <Dialog.Title className="text-sm font-semibold">Navigation</Dialog.Title>
            <Dialog.Close aria-label="Close navigation" className="inline-flex size-8 items-center justify-center rounded-lg text-muted hover:bg-hover hover:text-fg">
              <X aria-hidden className="size-4" />
            </Dialog.Close>
          </div>
          <div className="flex-1 overflow-y-auto px-3 py-4">
            <Sidebar groups={groups} collapsible onNavigate={() => setOpen(false)} />
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
