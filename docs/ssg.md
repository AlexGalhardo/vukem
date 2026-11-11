# Static site generation

Package `ssg` prerenders an `http.Handler` to static files. It is not a site generator in the Hugo sense, and it does not run the Inertia Node SSR server (`inertia/ssr.go` is unchanged). It performs one in-process GET per path you name and, when the response is 200, writes the body to disk.

Registered routes are not crawled. Only the paths you pass are rendered. The request has no cookies. Its host defaults to `ssg.invalid` (a reserved TLD). A handler can tell the call apart with `ssg.FromContext` and should not render per-user data: every visitor of the file would see it.

## Generate

```go
report, err := ssg.Generate(ctx, handler, []ssg.Page{
    {Path: "/"},
    {Path: "/about"},
    {Path: "/docs/cli"},
}, ssg.Options{OutDir: "dist"})
```

| Path | File |
| --- | --- |
| `/` | `dist/index.html` |
| `/about` or `/about/` | `dist/about/index.html` |
| `/docs/cli` | `dist/docs/cli/index.html` |

By default `Generate` renders into a sibling temporary directory (`<OutDir>.ssg-tmp-...`) and swaps it with `OutDir` by rename once every page returned 200. Stale pages from earlier runs disappear, a failed run leaves `OutDir` exactly as it was, and the swap writes a `.vukem-ssg` marker file. `Generate` replaces `OutDir` only when it is missing, empty or holds that marker; any other non-empty directory is `ErrNotGenerated` and nothing is touched. A symbolic link as `OutDir` is `ErrEscape`. The swap is two renames (`OutDir` aside, then the new directory in), so for a moment `OutDir` does not exist. If the process dies in between, `OutDir.ssg-old-...` holds the previous output and must be removed by hand. Concurrent runs on one `OutDir` are not supported: the last swap wins. Set `Options.NoClean` to write in place and never remove anything. `Report.Files` lists the relative paths (forward slashes) and byte counts of the pages that were written.

## What is refused

`Page.Path` must be an absolute URL path. A scheme, host, query, fragment, NUL, backslash, or any `..` segment is `ErrInvalidPath`. Percent-encoded dots (`%2e%2e`) are unescaped and rejected the same way. `/about/`, `/./foo` and repeated slashes are cleaned with `path.Clean` before the file name is chosen. A path is decoded once: a decoded path that still contains `%` (for example `/x%2541` or `/50%25`) is `ErrInvalidPath`, and the handler receives the decoded path (`/a%20b` is served as `/a b` and written to `a b/index.html`). A segment that ends in a dot or a space, or that is a Windows device name (`con`, `prn`, `aux`, `nul`, `com0`-`com9`, `lpt0`-`lpt9` and the superscript `com¹`-`com³`, `lpt¹`-`lpt³`, with or without an extension), or that ends in `.ssg-tmp` (the temporary file suffix), is `ErrInvalidPath`. Two pages whose output files differ only by case (`/About` and `/about`) are `ErrInvalidPath`, naming both paths, before anything is written.

`OutDir` is opened once with `os.Root`, and every directory and file is created through it. A link or junction inside `OutDir` that points elsewhere cannot redirect a write: the page is `ErrEscape` and nothing is created outside `OutDir`. Each page is written to a temporary file beside its target and renamed over it, so an existing hard link is replaced, not truncated.

Only status 200 is stored. Any other status, including a redirect, is `ErrStatus` and writes nothing for that page. A body over 8 MiB (`ssg.MaxBody`) is `ErrTooLarge` and writes nothing for that page. An exact 8 MiB body is kept. `Generate` stops at the first error. By default a failure leaves `OutDir` unchanged and the report is empty. With `Options.NoClean`, pages written earlier stay on disk and are still listed in the report. A cancelled context before the loop creates no files.

There is no socket and no redirect-following client. The call is `http.NewRequestWithContext` plus `ServeHTTP`.

## Command

`vukem ssg:generate` is a built-in app command. It uses the handler `App.Handler` already built. It does not boot a second framework and does not walk the route table.

```bash
vukem ssg:generate -out dist -path / -path /about
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-out` | `dist` | Output directory |
| `-path` | required, repeatable | One absolute URL path |
| `-no-clean` | `false` | Write in place and keep files from earlier runs |

A missing `-path` or an extra argument is a usage error (exit 2). A rejected path or a non-200 response is exit 1. Each written file is printed as `wrote <rel> (<n> bytes)`.
