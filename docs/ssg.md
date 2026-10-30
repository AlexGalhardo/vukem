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

`OutDir` is created when every path is valid. `Report.Files` lists the relative paths (forward slashes) and byte counts of the pages that were written.

## What is refused

`Page.Path` must be an absolute URL path. A scheme, host, query, fragment, NUL, backslash, or any `..` segment is `ErrInvalidPath`. Percent-encoded dots (`%2e%2e`) are unescaped and rejected the same way. `/about/`, `/./foo` and repeated slashes are cleaned with `path.Clean` before the file name is chosen.

The write is checked against `OutDir` after the path is cleaned, including the destination of a symlink. A path that would leave `OutDir` is `ErrEscape` and writes nothing.

Only status 200 is stored. Any other status, including a redirect, is `ErrStatus` and writes nothing for that page. A body over 8 MiB (`ssg.MaxBody`) is `ErrTooLarge` and writes nothing for that page. An exact 8 MiB body is kept. `Generate` stops at the first error. Pages written earlier stay on disk and are still listed in the report. A cancelled context before the loop creates no files.

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

A missing `-path` or an extra argument is a usage error (exit 2). A rejected path or a non-200 response is exit 1. Each written file is printed as `wrote <rel> (<n> bytes)`.
