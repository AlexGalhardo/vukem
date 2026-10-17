import Link from "next/link";
import { dict, localizeHref, type Locale } from "@/lib/i18n";

// Vukem mark: two slanted strokes forming a V on Go-gopher blue.
export function LogoMark({ className = "size-6" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" fill="none" aria-hidden className={className}>
      <rect width="32" height="32" rx="8" fill="#00ADD8" />
      <path d="M6.5 8h5.25l5.5 16h-5.25z" fill="#fff" />
      <path d="M20.25 8h5.25l-5.5 16h-5.25z" fill="#fff" fillOpacity=".72" />
      <path d="M15.25 24h1.75l1.2-3.5-.88-2.55z" fill="#007D9C" />
    </svg>
  );
}

export function Logo({ locale }: { locale: Locale }) {
  return (
    <Link
      href={localizeHref("/docs/introduction", locale)}
      className="flex items-center gap-2 rounded-md font-semibold tracking-tight text-fg"
      aria-label={dict(locale).logoLabel}
    >
      <LogoMark />
      <span className="text-[15px]">Vukem</span>
    </Link>
  );
}
