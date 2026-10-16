import Link from "next/link";

export function LogoMark({ className = "size-6" }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" fill="none" aria-hidden className={className}>
      <rect width="32" height="32" rx="8" fill="#0F766E" />
      <path d="M10 7h5v13h8v5H10z" fill="#fff" />
    </svg>
  );
}

export function Logo() {
  return (
    <Link href="/" className="flex items-center gap-2 rounded-md font-semibold tracking-tight text-fg" aria-label="Vukem home">
      <LogoMark />
      <span className="text-[15px]">Vukem</span>
    </Link>
  );
}
