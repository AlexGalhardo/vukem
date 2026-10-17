/**
 * UI locales. English is the default and is served without a prefix
 * (/docs/...); every other locale lives under /<id>/docs/... Client-safe:
 * no file system access here.
 */
export const locales = ["en", "pt-br"] as const;
export type Locale = (typeof locales)[number];

export const defaultLocale: Locale = "en";

type LocaleInfo = {
  /** BCP 47 tag for <html lang>, hreflang and Intl. */
  tag: string;
  /** Name of the language in itself, for the switcher. */
  name: string;
  /** Short label shown in the switcher. */
  short: string;
  /** Time zone used to print "Last updated". */
  timeZone: string;
};

export const localeInfo: Record<Locale, LocaleInfo> = {
  en: { tag: "en", name: "English", short: "EN", timeZone: "UTC" },
  "pt-br": { tag: "pt-BR", name: "Português (Brasil)", short: "PT", timeZone: "America/Sao_Paulo" },
};

const dictionaries = {
  en: {
    skipToContent: "Skip to content",
    logoLabel: "Vukem documentation",
    githubTooltip: "GitHub repository",
    githubLabel: "Vukem on GitHub",
    language: "Language",
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
    notTranslated: "",
    readInEnglish: "",
  },
  "pt-br": {
    skipToContent: "Pular para o conteúdo",
    logoLabel: "Documentação do Vukem",
    githubTooltip: "Repositório no GitHub",
    githubLabel: "Vukem no GitHub",
    language: "Idioma",
    openNavigation: "Abrir navegação",
    closeNavigation: "Fechar navegação",
    navigation: "Navegação",
    documentation: "Documentação",
    onThisPage: "Nesta página",
    colorTheme: "Tema de cores",
    lightTheme: "Tema claro",
    darkTheme: "Tema escuro",
    systemTheme: "Tema do sistema",
    lastUpdated: "Última atualização",
    editOnGitHub: "Editar esta página no GitHub",
    pagination: "Paginação",
    previous: "Anterior",
    next: "Próxima",
    notTranslated: "Esta página ainda não foi traduzida. O conteúdo abaixo está em inglês.",
    readInEnglish: "Ler em inglês",
  },
} satisfies Record<Locale, Record<string, string>>;

export type Dictionary = (typeof dictionaries)["en"];

export function dict(locale: Locale): Dictionary {
  return dictionaries[locale];
}

/** URL prefix of a locale: "" for English, "/pt-br" otherwise. */
export function localePrefix(locale: Locale): string {
  return locale === defaultLocale ? "" : `/${locale}`;
}

/** Moves an English /docs/... link into locale; other links are unchanged. */
export function localizeHref(href: string, locale: Locale): string {
  if (locale === defaultLocale || !(href === "/docs" || href.startsWith("/docs/") || href.startsWith("/docs#"))) return href;
  return `${localePrefix(locale)}${href}`;
}

/** Splits a pathname into its locale and the English pathname. */
export function splitLocale(pathname: string): { locale: Locale; path: string } {
  for (const l of locales) {
    const prefix = localePrefix(l);
    if (prefix && (pathname === prefix || pathname.startsWith(`${prefix}/`))) {
      return { locale: l, path: pathname.slice(prefix.length) || "/" };
    }
  }
  return { locale: defaultLocale, path: pathname };
}

/** "Last updated" timestamp in the locale's language and time zone. */
export function formatUpdated(iso: string, locale: Locale): string {
  const { tag, timeZone } = localeInfo[locale];
  return new Intl.DateTimeFormat(tag, {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    timeZone,
    timeZoneName: "short",
  }).format(new Date(iso));
}
