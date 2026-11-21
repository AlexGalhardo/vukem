import type { ReactElement } from "react";

// Small inline icons drawn for this project, so the build ships no icon font
// or third-party image. Each is decorative: the text next to it carries the meaning.

interface IconProps {
  className?: string;
}

function Svg({ className, children }: IconProps & { children: ReactElement | ReactElement[] }): ReactElement {
  return (
    <svg
      className={className}
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}

export function SunIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <circle cx="8" cy="8" r="3" />
      <path d="M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M12.6 3.4l-1 1M4.4 11.6l-1 1" />
    </Svg>
  );
}

export function MoonIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <path d="M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5a5.5 5.5 0 1 0 7 7z" />
    </Svg>
  );
}

export function TableIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <rect x="2" y="3" width="12" height="10" rx="1.5" />
      <path d="M2 6.5h12M6 6.5V13" />
    </Svg>
  );
}

export function DatabaseIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <ellipse cx="8" cy="4" rx="5" ry="2" />
      <path d="M3 4v8c0 1.1 2.2 2 5 2s5-.9 5-2V4M3 8c0 1.1 2.2 2 5 2s5-.9 5-2" />
    </Svg>
  );
}

export function KeyboardIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <rect x="1.5" y="4" width="13" height="8" rx="1.5" />
      <path d="M4 7h.01M6.5 7h.01M9 7h.01M11.5 7h.01M5 9.5h6" />
    </Svg>
  );
}

export function SortIcon({ className, direction }: IconProps & { direction: "asc" | "desc" }): ReactElement {
  return (
    <Svg className={className}>
      {direction === "asc" ? <path d="M8 13V3M4 7l4-4 4 4" /> : <path d="M8 3v10M4 9l4 4 4-4" />}
    </Svg>
  );
}

export function ExpandIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <path d="M9.5 2.5h4v4M6.5 13.5h-4v-4M13.5 2.5 9 7M2.5 13.5 7 9" />
    </Svg>
  );
}

export function LinkIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <path d="M6.5 9.5l3-3M7 4.5l1-1a2.5 2.5 0 0 1 3.5 3.5l-1 1M9 11.5l-1 1A2.5 2.5 0 0 1 4.5 9l1-1" />
    </Svg>
  );
}

export function CloseIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
    </Svg>
  );
}

export function PlusIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <path d="M8 3v10M3 8h10" />
    </Svg>
  );
}

export function RefreshIcon({ className }: IconProps): ReactElement {
  return (
    <Svg className={className}>
      <path d="M13 8a5 5 0 1 1-1.5-3.6M13 2.5v3h-3" />
    </Svg>
  );
}
