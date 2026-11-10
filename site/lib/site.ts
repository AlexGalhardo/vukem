export const site = {
  name: "Vukem",
  url: process.env.SITE_URL ?? process.env.NEXT_PUBLIC_SITE_URL ?? "https://vukem.dev",
  repo: "https://github.com/AlexGalhardo/vukem",
  editBase: "https://github.com/AlexGalhardo/vukem/edit/main/site/content",
} as const;
