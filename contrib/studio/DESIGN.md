# Studio design

The Studio interface is an original design made for Vukem. This file records
the brief, the tokens and the rules that keep it original.

## Brief

- Calm: neutral surfaces, one accent color, no decoration that carries no
  information.
- Dense: small type scale, tight spacing, tables and lists first.
- Keyboard-first: every action is reachable from the keyboard, with a visible
  focus ring; the mouse and screen readers reach the same actions.

## Originality rule

No code, CSS, asset, icon, name, layout, wording or screenshot of another
product is used, copied, traced or stored in this project. Market knowledge
informs which workflows exist (a grid, filters, a SQL editor, tabs); it never
supplies how they look or what they are called. Icons are small inline SVG
components written in this project. The build includes no font or icon file
that the author has not approved. When in doubt, design it from the workflow,
not from another product.

## Tokens

The tokens are CSS custom properties in `web/src/styles/tokens.css`. They are
this project's own values.

| Token group | Values |
| --- | --- |
| Surfaces | `--surface`, `--surface-raised`, `--surface-sunken`, `--border` |
| Text | `--text`, `--text-muted`, `--text-on-accent` |
| Accent | `--accent`, `--accent-hover` (a teal) |
| Status | `--ok`, `--warn`, `--danger` |
| Type scale | 12, 13, 14, 16, 20, 28 px; system UI font stack, system monospace |
| Spacing | 4 px grid: 4, 8, 12, 16, 24, 32 |
| Radius | 4 px controls, 8 px panels |

Light and dark palettes follow `prefers-color-scheme` and a manual toggle
(`data-theme` on the root element). Motion is off under
`prefers-reduced-motion`.

**The dark palette is neutral.** Page, panels, grid, inputs, menus and dialogs
use true black and neutral grays with no hue (equal red, green and blue
channels); the teal appears only as the accent (buttons, focus ring, links,
selection). Nothing in the dark theme is tinted green. The light palette is
unchanged.

| Dark token | Value | Contrast on `--surface` / `--surface-raised` |
| --- | --- | --- |
| `--surface` | `#000000` | |
| `--surface-sunken` | `#0a0a0a` | |
| `--surface-raised` | `#141414` | |
| `--border` | `#333333` | decorative separator |
| `--text` | `#ededed` | 17.9 / 15.7 |
| `--text-muted` | `#a3a3a3` | 8.3 / 7.3 |
| `--accent` | `#2dd4bf` | 11.3 / 9.9 |
| `--text-on-accent` | `#000000` | 11.3 on `--accent` |
| `--ok`, `--warn`, `--danger` | `#4ade80`, `#fbbf24`, `#f87171` | 12.1, 12.6, 7.6 on `--surface` |

Every text pair holds WCAG AA (4.5:1) on the three surfaces.

## Constraints from the security model

The Content Security Policy allows only same-origin scripts and styles. So the
interface uses no CSS-in-JS runtime, no inline `<style>`, no `style=""`
attribute in markup and no `eval`.

## The data grid

The table view is one virtualized grid, written for this project (no
virtualization library).

- **Window math** is a pure module (`web/src/grid/virtual.ts`): from the scroll
  position it returns the rows and columns to draw, with an overscan of 6 rows
  and 2 columns. Rows have a fixed height of 28, 32 or 40 px, so nothing is
  measured. The DOM holds a screen of rows, never more than about 100, however
  large the table. Columns are windowed only above 30. A table taller than 10
  million pixels is scrolled through a scaled range, because browsers cap the
  height of an element.
- **Placement under the CSP.** Rows get `transform: translateY(...)` and cells
  a width from the script through the CSSOM (React's `style` prop sets
  `element.style`), which `style-src 'self'` allows. There is still no
  `style=""` in the served markup and no `<style>` element:
  `TestBuiltUIIsCSPClean` and `e2e/grid.spec.ts` (no console error) check it.
- **Paging.** `pageCache.ts` keeps pages of 100 rows. The default order uses the
  server's keyset cursor and loads in order, so "last row" reads the table from
  its end (the sort reversed, after an exact count). Any other sort uses offset
  pages (random access, up to page 1000 of the server, so 100,000 rows). Stale
  requests are aborted, at most 3 run at once, at most 50 pages stay in memory.
- **Semantics.** The grid is an ARIA `grid`: `aria-rowcount` is the rows plus the
  header row, `aria-colcount` the columns plus the row gutter, every row and cell
  carries `aria-rowindex` and `aria-colindex`, and there is one tab stop (the
  active cell moves with the arrow keys). A polite live region announces
  position in steps of 100 rows, selection, copy and save results.
- **Write mode** changes nothing on screen unless Studio started with
  `--write`: without it there is no editor, no New row, no Delete, and the
  write routes do not exist (404). An edit reads the full value first, sends it
  back as `expect`, and the server refuses (409) when the row changed
  meanwhile. A table without a primary key is read-only.
- **Shortcuts** live in `web/src/shortcuts/`: single plain keys (`/`, `r`,
  `n`, `?`, `]`, `[`) and two-key chords (`q` then `n`, `w` then `c`), never a
  combination the browser owns (`mod+T`, `mod+W` and `mod+N` are never used), and
  never while the focus is in an input, textarea, select or editable element.
  The one exception is the help chord `Ctrl+K` (`Cmd+K` on Apple platforms):
  the header's "Shortcuts" button advertises it and it opens the same help
  dialog from anywhere, also inside a field, since a modified key is never text.
  It overrides the browsers' search-bar key only while the page has focus, and
  nothing else binds it. The SQL editor is a typing field, so inside it only its own documented chords
  apply (see below); they are listed in the help dialog.

## The SQL console

The console is a workspace of tabs (`web/src/tabs/`), each a query or a table,
with an editor (`web/src/editor/`), a runner and a result view
(`web/src/console/`), and a history and library (`web/src/history/`). It adds
no dependency: there is no editor library.

- **The editor** is a real `textarea` (selection, IME, undo, mobile and
  assistive technology work as they do everywhere) over a highlight layer drawn
  from a small SQL tokenizer. Font, line height, padding and tab size are the
  same in both layers, which is the only thing that keeps them aligned. Both the
  layer and the line numbers draw only the lines in view, from a per-line cache
  (`LineCache`) that redoes a line, and the lines below it only when what is
  open (a quote, a comment) moved, so typing in a 5,000-line script costs about
  a line (median 7 ms in the browser, against 3 ms for a bare textarea).
  Completion reads 100 lines around the cursor, not the whole script. The layer
  is `aria-hidden`; in forced-colors mode it steps aside and the textarea draws
  its own text. Wrapping is off.
- **Keys of the editor:** `Ctrl` or `Cmd` + `Enter` runs the selection or the
  statement at the cursor, with `Shift` all of it as one statement; `/` toggles
  a comment; `S` saves the query; `F` finds (the last two only while the focus
  is inside the editor, so the browser keeps them elsewhere); `Ctrl+Space`
  completes. `Tab` indents unless `Escape` came first, so the editor is not a
  keyboard trap.
- **Read-only first.** The server refuses a write that was not asked for
  (a second statement, a comment hiding one, a `WITH` that writes, `SELECT ...
  INTO`, `EXPLAIN ANALYZE` of a write, `ATTACH`, a setting `PRAGMA`), whatever
  the interface says. The interface classifies a statement itself only to warn
  before sending and to decide when to ask. With `--write`, a tab has a Write
  mode switch (off whenever the tab opens); running a non-read statement then
  opens a confirmation with the statement and the source and a button named for
  the statement (`Run UPDATE`); `DROP`, `TRUNCATE` and an `UPDATE` or `DELETE`
  with no `WHERE` also need the source's name typed.
- **Runs** can be cancelled (the request is aborted and the server stops the
  statement), have a 30 second timeout, and return at most 1000 rows and about
  8 MiB of cell text. Rows show in the same virtualized grid as the table view,
  sortable, with copy and export of what was loaded.
- **Tabs** are an ARIA tablist with roving tabindex (Arrow, Home, End, `Delete`
  to close, `Alt`+Arrow to reorder, drag with the mouse, middle click closes),
  at most 20 per source. Open tabs and the text of each query are kept in
  `sessionStorage` per source, so a reload restores them and closing the browser
  tab clears them. A tab holds a table name or the text of a query, never a
  result.
- **History and the library are private to this browser.** Every run is kept
  in `localStorage` under a key that includes the Studio run (`sessionKey`), and
  the keys of earlier runs are deleted at startup: the statement, source, time,
  duration, rows or error kind, and verb (never the rows), 200 at most. The
  history can be cleared, searched, and switched off (which also clears it), and
  its panel says it holds values until cleared. "Open in new tab" never runs the
  statement. Saved queries (100 at most) live under one stable key. Nothing is
  sent anywhere and a query is never put in a URL.
- **Logs.** A console failure is logged by its class (`query_failed`,
  `sqlstate_*`, `canceled`, `timeout`), never by the driver's text, which can
  quote the very token that broke the statement. The audit of a write records
  only the size of the statement.

## The schema view

The schema of a SQL source (`web/src/schema/`) is a left list of tables with a
filter box, a detail pane for the selected table (columns with type, null,
default and key badges; indexes in order with their unique, primary and
composite kind; foreign keys; "referenced by"), the relationship diagram and
the same relationships as a table. It reads `GET /schema`, which comes from the
database catalog alone.

- **The layout is a pure function** (`layout.ts`): the same schema always draws
  the same picture. A table sits one layer to the right of the deepest table it
  references; a foreign key that would close a cycle is drawn from the right
  side of both tables; inside a layer the order is improved by the barycenter of
  the neighbours in a fixed number of sweeps, ties breaking on position and
  name; a layer longer than 16 tables is split in columns. Edges are
  orthogonal, with plain arrow heads and a lane in the gap for each, so two
  edges do not share a vertical run. No force simulation, no randomness, no
  clock. 300 tables lay out in a few milliseconds (a test holds it under 500).
- **Pan and zoom** (`viewport.ts`) never hijack the page: drag or the arrow keys
  to pan, `+`, `-` and `0` or the buttons to zoom and fit; the mouse wheel
  scrolls the page. Every table is a focusable link (Enter opens it in a tab);
  focusing one highlights its neighbours and dims the rest.
- **A picture is not accessible**, so the Relationships table lists every
  foreign key (from table, columns, to table, columns), the diagram has a text
  description, and it can be hidden. It draws at most 300 tables; a switch keeps
  only the selected table and its neighbours.
- **CREATE statement**: offered only where the database reports it through a
  read-only query (SQLite, MySQL); shown in a read-only block that can be copied.

## MongoDB

A collection (`web/src/mongo/`) is a virtualized list of documents read by
cursor (`_id`) or, sorted by a field, by page up to the server's cap, which the
page says in words. The Table view infers its columns from the top-level fields
of the first 100 documents and shows nested values as compact JSON; the JSON
view is one summary column. The filter bar takes a field path, an operator from
the fixed table and a scalar: the server builds the MongoDB filter, and nothing
typed is ever an operator or an object. A document opens in a drawer as a
collapsible tree or as raw Extended JSON.

The console statement is one JSON object, `{"collection": ..., "filter": ...}`
for a find or `{"collection": ..., "pipeline": [...]}` for an aggregate. The
editor is the same real-textarea editor with a JSON language: highlighting,
bracket matching, completion of collection names and stage names from lists the
page has. The text is checked as JSON before it is sent, and `$where`,
`$function`, `$accumulator`, `$out` and `$merge` are refused early with a
reason; the server checks again and does not depend on that. With `--write`,
insert, replace and delete exist; replace and delete ask for the `_id` to be
typed, and an aggregate that writes asks for the source name.

## Redis

Keys (`web/src/redis/`) are listed with `SCAN`, one step at a time: "Load more"
takes the next step until the cursor comes back as 0, a repeated key is shown
once, and a note says in plain words that `SCAN` is incremental and can miss
keys created while it runs. A key opens by its type through the same grid: a
string as text or as a hex dump when it is binary, a hash as field and value, a
list as index and value, a set as members, a sorted set as member and score, a
stream as id and fields, a page of 200 at a time with a visible count. With
`--write` a string or a hash field is edited after a confirmation, the expiry
is set or removed, and a key is deleted only after its name is typed. The
console takes a fixed list of commands, completes them, refuses the rest with
the server's own message, and shows a reply as text or as a table.

## One interface

The old server-rendered pages are gone: `/` redirects to `/ui/`, and every path
of the old interface answers 404 behind the same guards. The consoles of the
three kinds of source share the tab strip, the runner (a `Protocol` says which
route a statement goes to and how its answer becomes a result), the result
view, the history and the confirmation dialogs.
