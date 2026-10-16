package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
)

// The debug error page (Services.Debug, 5xx, browsers): the error chain,
// stack frames with source code, the request with credentials redacted,
// and the route. It is never rendered in production (Debug is off there).

// redactedHeaders never appear on the debug page or in debug reports.
var redactedHeaders = []string{
	"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "X-Auth-Token",
	"X-Csrf-Token", "X-Xsrf-Token",
}

const (
	frameApp       = "app"
	frameFramework = "framework"
	frameStdlib    = "stdlib"
)

type debugLine struct {
	N    int
	Text string
	Hot  bool
}

type debugFrame struct {
	Function string
	File     string
	Line     int
	Kind     string
	Source   []debugLine
}

type debugLink struct{ Type, Message string }

type debugData struct {
	Status    int
	Title     string
	Type      string
	Message   string
	Chain     []debugLink
	Frames    []debugFrame
	Method    string
	URL       string
	Route     string
	RequestID string
	Headers   [][2]string
	Go        string
	Nonce     string
}

func (c *Ctx) debugData(status int, err error) debugData {
	d := debugData{
		Status:    status,
		Title:     http.StatusText(status),
		Type:      fmt.Sprintf("%T", rootCause(err)),
		Message:   err.Error(),
		Method:    c.r.Method,
		URL:       c.r.URL.RequestURI(),
		Route:     c.route,
		RequestID: RequestID(c.Context()),
		Go:        runtime.Version(),
		Nonce:     CSPNonce(c.Context()),
	}

	for e := err; e != nil; e = errors.Unwrap(e) {
		d.Chain = append(d.Chain, debugLink{Type: fmt.Sprintf("%T", e), Message: e.Error()})
	}

	sources := map[string][]string{}

	for _, f := range errs.Stack(err) {
		df := debugFrame{Function: f.Function, File: f.File, Line: f.Line, Kind: frameKind(f.Function)}
		if df.Kind != frameStdlib {
			df.Source = snippet(sources, f.File, f.Line, 5)
		}

		d.Frames = append(d.Frames, df)
	}

	keys := make([]string, 0, len(c.r.Header))
	for k := range c.r.Header {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	for _, k := range keys {
		v := strings.Join(c.r.Header.Values(k), ", ")
		if slices.ContainsFunc(redactedHeaders, func(h string) bool { return strings.EqualFold(h, k) }) {
			v = "[redacted]"
		}

		d.Headers = append(d.Headers, [2]string{k, v})
	}

	return d
}

func rootCause(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}

		err = next
	}
}

// frameKind classifies a frame by its function's import path: the
// standard library has no dot in its first path element.
func frameKind(fn string) string {
	switch {
	case strings.HasPrefix(fn, "github.com/AlexGalhardo/vukem/"),
		strings.HasPrefix(fn, "github.com/AlexGalhardo/vukem."):
		return frameFramework
	case !strings.Contains(strings.SplitN(fn, "/", 2)[0], "."):
		return frameStdlib
	default:
		return frameApp
	}
}

// snippet returns the lines around line of file, or nil when it cannot be
// read (deployed binaries have no sources).
func snippet(cache map[string][]string, file string, line, around int) []debugLine {
	lines, ok := cache[file]
	if !ok {
		b, err := os.ReadFile(file)
		if err == nil && len(b) < 1<<20 {
			lines = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		}

		cache[file] = lines
	}

	if line < 1 || line > len(lines) {
		return nil
	}

	var out []debugLine

	for n := max(1, line-around); n <= min(len(lines), line+around); n++ {
		out = append(out, debugLine{N: n, Text: lines[n-1], Hot: n == line})
	}

	return out
}

func (c *Ctx) renderDebugPage(status int, err error) {
	var buf bytes.Buffer
	if terr := debugPage.Execute(&buf, c.debugData(status, err)); terr != nil {
		c.Log().ErrorContext(c.Context(), "render debug page", "err", terr)
		return
	}

	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.w.Header().Set("Cache-Control", "no-store")
	c.w.WriteHeader(status)
	_, _ = c.w.Write(buf.Bytes())
}

// writeDebugReport prints a colored summary of err to Services.DebugOut.
func (c *Ctx) writeDebugReport(status int, err error) {
	out := c.s.DebugOut
	if out == nil {
		return
	}

	d := c.debugData(status, err)

	const (
		red   = "\x1b[41;97;1m"
		bold  = "\x1b[1m"
		dim   = "\x1b[2m"
		hot   = "\x1b[31m"
		reset = "\x1b[0m"
	)

	var b strings.Builder

	fmt.Fprintf(&b, "\n%s %d %s %s %s %s\n", red, d.Status, d.Title, reset, d.Method, d.URL)

	for i, l := range d.Chain {
		prefix := "  "
		if i > 0 {
			prefix = "    caused by "
		}

		fmt.Fprintf(&b, "%s%s%s%s: %s\n", prefix, bold, l.Type, reset, l.Message)
	}

	for _, f := range d.Frames {
		if f.Kind != frameApp {
			continue
		}

		fmt.Fprintf(&b, "\n  at %s%s%s %s(%s:%d)%s\n", bold, f.Function, reset, dim, f.File, f.Line, reset)

		for _, l := range f.Source {
			if l.Hot {
				fmt.Fprintf(&b, "  %s> %4d | %s%s\n", hot, l.N, l.Text, reset)
			} else {
				fmt.Fprintf(&b, "  %s  %4d | %s%s\n", dim, l.N, l.Text, reset)
			}
		}
	}

	b.WriteString("\n")
	_, _ = io.WriteString(out, b.String())
}

var debugPage = template.Must(template.New("debug").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Status}} {{.Title}}</title>
<style{{with .Nonce}} nonce="{{.}}"{{end}}>
:root { color-scheme: light dark; --bg: #fff; --fg: #18181b; --muted: #71717a; --card: #f4f4f5; --line: #e4e4e7;
  --accent: #dc2626; --hot: #fee2e2; }
@media (prefers-color-scheme: dark) { :root { --bg: #09090b; --fg: #f4f4f5; --muted: #a1a1aa; --card: #18181b;
  --line: #27272a; --hot: #450a0a; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--fg); font: 15px/1.5 system-ui, sans-serif; }
main { max-width: 64rem; margin: 0 auto; padding: 2rem 1rem; }
.badge { display: inline-block; background: var(--accent); color: #fff; border-radius: .3rem; padding: .1rem .5rem; font-weight: 600; }
h1 { font-size: 1.4rem; margin: .5rem 0; word-break: break-word; }
.type { color: var(--muted); font-family: ui-monospace, monospace; }
section { margin-top: 2rem; }
h2 { font-size: 1rem; text-transform: uppercase; letter-spacing: .05em; color: var(--muted); }
ol.chain { padding-left: 1.2rem; } ol.chain li { margin: .3rem 0; }
code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }
details { background: var(--card); border: 1px solid var(--line); border-radius: .5rem; margin: .5rem 0; }
summary { cursor: pointer; padding: .6rem .8rem; }
summary .file { color: var(--muted); }
pre { margin: 0; padding: .5rem 0; overflow-x: auto; border-top: 1px solid var(--line); }
pre span { display: block; padding: 0 .8rem; white-space: pre; }
pre span.hot { background: var(--hot); }
pre i { color: var(--muted); font-style: normal; display: inline-block; width: 3.5rem; user-select: none; }
.kind { font-size: .75rem; color: var(--muted); margin-left: .4rem; }
table { border-collapse: collapse; width: 100%; } td { border-top: 1px solid var(--line); padding: .3rem .5rem; vertical-align: top; word-break: break-all; }
td:first-child { color: var(--muted); width: 14rem; }
</style>
</head>
<body>
<main>
<span class="badge">{{.Status}} {{.Title}}</span>
<h1>{{.Message}}</h1>
<div class="type">{{.Type}}</div>

<section>
<h2>Error chain</h2>
<ol class="chain">{{range .Chain}}<li><code>{{.Type}}</code> {{.Message}}</li>{{end}}</ol>
</section>

<section>
<h2>Stack</h2>
{{range .Frames}}<details{{if eq .Kind "app"}} open{{end}}>
<summary><code>{{.Function}}</code><span class="kind">{{.Kind}}</span><br><span class="file">{{.File}}:{{.Line}}</span></summary>
{{if .Source}}<pre>{{range .Source}}<span{{if .Hot}} class="hot"{{end}}><i>{{.N}}</i>{{.Text}}</span>{{end}}</pre>{{end}}
</details>
{{else}}<p>No stack trace: return errors from errs.New, errs.Wrap or errs.WithStack to get one.</p>{{end}}
</section>

<section>
<h2>Request</h2>
<table>
<tr><td>Method</td><td>{{.Method}}</td></tr>
<tr><td>URL</td><td>{{.URL}}</td></tr>
{{with .Route}}<tr><td>Route</td><td>{{.}}</td></tr>{{end}}
{{with .RequestID}}<tr><td>Request ID</td><td>{{.}}</td></tr>{{end}}
{{range .Headers}}<tr><td>{{index . 0}}</td><td>{{index . 1}}</td></tr>{{end}}
</table>
</section>

<section>
<h2>Application</h2>
<table><tr><td>Go</td><td>{{.Go}}</td></tr></table>
<p class="type">Shown because APP_DEBUG=true outside production.</p>
</section>
</main>
</body>
</html>
`))
