import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { CodeBlock } from "@/components/mdx/code-block";
import { LogoMark } from "@/components/logo";
import { getNavGroups } from "@/lib/content";
import { site } from "@/lib/site";

const SAMPLE = `func Web(r *router.Router, app *lumek.App) error {
	if err := lumek.DefaultMiddleware(app, r); err != nil {
		return err
	}

	db, err := app.DB()
	if err != nil {
		return err
	}

	r.Get("/", controllers.Home).Name("home")
	r.Resource("posts", controllers.NewPostController(db)) // posts.index, posts.store…

	return nil
}

func (pc *PostController) Store(c *web.Ctx) error {
	in, err := web.Parse(c, requests.PostSchema) // typed input or 422 / redirect back
	if err != nil {
		return err
	}

	post := models.Post{Title: in.Title, Body: in.Body}
	if err := orm.Create(c.Context(), pc.db, &post); err != nil {
		return err
	}

	return c.RedirectRoute("posts.show", "post", post.ID)
}
`;

const FEATURES = [
  { title: "net/http first", text: "Handlers and middleware are plain net/http. Any http.Handler mounts on the router." },
  { title: "No magic", text: "No globals, no init() registration, no reflection-based dependency injection." },
  { title: "Typed end to end", text: "Typed config structs, named routes checked at boot, Zod-style schemas for input." },
  { title: "Secure by default", text: "Security headers, CSP nonces, deny-by-default CORS, fail-closed boot checks." },
];

export default function Home() {
  const groups = getNavGroups().slice(0, 3);
  return (
    <main id="content" className="mx-auto max-w-5xl px-4 py-16 sm:px-6 sm:py-24">
      <section className="max-w-2xl">
        <LogoMark className="mb-6 size-10" />
        <p className="mb-3 inline-flex items-center gap-2 rounded-full border border-border px-3 py-1 text-xs text-muted">
          <span className="size-1.5 rounded-full bg-amber-500" aria-hidden /> Pre-1.0: APIs may change between v0.x releases
        </p>
        <h1 className="text-4xl font-semibold tracking-tight text-fg sm:text-5xl">A Go web framework for REST APIs and full-stack apps.</h1>
        <p className="mt-5 text-lg leading-relaxed text-muted">{site.tagline} Routing, validation, an ORM, authentication, problem+json errors and HTMX-first views, driven by one CLI.</p>
        <div className="mt-8 flex flex-wrap gap-3">
          <Link href="/docs/introduction" className="inline-flex h-10 items-center gap-2 rounded-lg bg-fg px-4 text-sm font-medium text-bg transition-opacity hover:opacity-90">
            Read the docs <ArrowRight aria-hidden className="size-4" />
          </Link>
          <a href={site.repo} target="_blank" rel="noreferrer" className="inline-flex h-10 items-center rounded-lg border border-border px-4 text-sm font-medium text-fg transition-colors hover:bg-hover">
            GitHub
          </a>
        </div>
      </section>

      <section className="mt-16" aria-label="Example">
        <CodeBlock code={SAMPLE} lang="go" title="routes.go" />
      </section>

      <section className="mt-16 grid gap-px overflow-hidden rounded-xl border border-border bg-border sm:grid-cols-2" aria-label="Principles">
        {FEATURES.map((f) => (
          <div key={f.title} className="bg-bg p-6">
            <h2 className="text-sm font-semibold text-fg">{f.title}</h2>
            <p className="mt-1.5 text-sm leading-relaxed text-muted">{f.text}</p>
          </div>
        ))}
      </section>

      <section className="mt-16 grid gap-8 sm:grid-cols-3" aria-label="Start here">
        {groups.map((g) => (
          <div key={g.title}>
            <h2 className="text-xs font-semibold tracking-wide text-fg">{g.title}</h2>
            <ul className="mt-3 space-y-2">
              {g.items.slice(0, 5).map((i) => (
                <li key={i.href}>
                  <Link href={i.href} className="text-sm text-muted transition-colors hover:text-fg">
                    {i.title}
                  </Link>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </section>
    </main>
  );
}
