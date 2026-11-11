# Studio styles

`studio.input.css` is the source of `../assets/studio.css`, which is built with
the Tailwind CSS v4 standalone CLI and **committed**: the binary does not
need Node or Tailwind to build. Rebuild after changing `studio.input.css`, the
templates or `assets/studio.js`, and commit the result.

## Building

The task never downloads anything. You download the Tailwind CLI once and point
`TAILWIND_BIN` at it:

1. Download `tailwindcss-<platform>` for **v4.3.5** from
   <https://github.com/tailwindlabs/tailwindcss/releases/tag/v4.3.5> (for
   example `tailwindcss-windows-x64.exe`, `tailwindcss-linux-x64`,
   `tailwindcss-macos-arm64`).
2. Check its SHA-256 against `sha256sums.txt` on the same release page.
   The Windows x64 file of v4.3.5 is
   `2803583268c53f4713e89943e211081fddb186c0a4a7d07b5c342c6ae853ec95`.
3. Run, from the repository root:

```sh
TAILWIND_BIN=/path/to/tailwindcss task studio:css
```

which runs `tailwindcss -i contrib/studio/styles/studio.input.css -o contrib/studio/assets/studio.css --minify`
(`-i`, `-o` and `--minify` come from the CLI's own `--help`). The output is
deterministic: two runs with the same CLI version produce the same file, so a
diff after a rebuild is a real change.

## Sources

- Tailwind CSS standalone CLI: <https://tailwindcss.com/docs/installation/tailwind-cli>
  (links the executable to the GitHub releases page).
- Theme tokens: the shadcn/ui "neutral" theme, <https://ui.shadcn.com/docs/theming>
  (the `:root` and `.dark` blocks, fetched 2026-11-11).
