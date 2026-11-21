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
