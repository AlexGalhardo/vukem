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
- **Shortcuts** live in `web/src/shortcuts/`: single plain keys only (`/`, `r`,
  `n`, `?`), never a combination the browser owns, and never while the focus is
  in an input, textarea, select or editable element.
