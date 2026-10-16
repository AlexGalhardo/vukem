"use client";

import { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { toastManager } from "@/components/toaster";

/** Copies `text` with clipboard.js and confirms with a toast. */
export function CopyButton({ text, className = "" }: { text: string; className?: string }) {
  const ref = useRef<HTMLButtonElement>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let destroyed = false;
    let instance: { destroy(): void } | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;

    import("clipboard").then(({ default: ClipboardJS }) => {
      if (destroyed) return;
      const cb = new ClipboardJS(el, { text: () => text });
      cb.on("success", (e) => {
        e.clearSelection();
        setCopied(true);
        clearTimeout(timer);
        timer = setTimeout(() => setCopied(false), 1500);
        toastManager.add({ title: "Copied to clipboard" });
      });
      cb.on("error", () => toastManager.add({ title: "Press Ctrl+C to copy" }));
      instance = cb;
    });

    return () => {
      destroyed = true;
      clearTimeout(timer);
      instance?.destroy();
    };
  }, [text]);

  return (
    <button
      ref={ref}
      type="button"
      aria-label={copied ? "Copied" : "Copy code"}
      className={`inline-flex size-7 items-center justify-center rounded-md text-faint transition-colors hover:bg-hover hover:text-fg ${className}`}
    >
      {copied ? <Check aria-hidden className="size-3.5 text-accent" /> : <Copy aria-hidden className="size-3.5" />}
    </button>
  );
}
