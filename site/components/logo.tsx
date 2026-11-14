import { Link } from "@/components/link";
import { t } from "@/lib/ui-text";
import gopher from "@/src/assets/gopher.svg?raw";

// The shared Go gopher (src/assets/gopher.svg) is the mark; only the landing copy animates.
export function LogoMark({ className = "size-6" }: { className?: string }) {
  return <span aria-hidden className={`gopher ${className}`} dangerouslySetInnerHTML={{ __html: gopher }} />;
}

export function Logo() {
  return (
    <Link
      href="/"
      className="flex items-center gap-2 rounded-md font-semibold tracking-tight text-fg"
      aria-label={t.logoLabel}
    >
      <LogoMark />
      <span className="text-[0.9375rem]">Vukem</span>
    </Link>
  );
}
