/** Interface strings. The site is English only. Client-safe: no file system access here. */
export const t = {
  skipToContent: "Skip to content",
  logoLabel: "Vukem documentation",
  githubTooltip: "GitHub repository",
  githubLabel: "Vukem on GitHub",
  changelog: "Changelog",
  changelogTitle: "Changelog",
  changelogDescription: "Every release of Vukem, with a link to the commit behind each change.",
  openNavigation: "Open navigation",
  closeNavigation: "Close navigation",
  navigation: "Navigation",
  documentation: "Documentation",
  onThisPage: "On this page",
  colorTheme: "Color theme",
  lightTheme: "Light theme",
  darkTheme: "Dark theme",
  systemTheme: "System theme",
  lastUpdated: "Last updated",
  editOnGitHub: "Edit this page on GitHub",
  pagination: "Pagination",
  previous: "Previous",
  next: "Next",
} as const;

/** "Last updated" timestamp, printed in UTC. */
export function formatUpdated(iso: string): string {
  return new Intl.DateTimeFormat("en", {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    timeZone: "UTC",
    timeZoneName: "short",
  }).format(new Date(iso));
}
