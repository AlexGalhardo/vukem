# Terminal recordings

GIFs embedded in the main README, recorded with [VHS](https://github.com/charmbracelet/vhs) from the tapes in `tapes/`.

| GIF | Tape | Shows |
| --- | --- | --- |
| `new-api.gif` | `tapes/new-api.tape` | `vukem new shop --api`, `ls`, `migrate` + `db:seed`, `vukem serve` and `curl` the posts endpoint |
| `new-htmx.gif` | `tapes/new-htmx.tape` | `vukem new site` (answering the stack prompt with 1), `ls` and `vukem route:list` |
| `new-inertia-react.gif` | `tapes/new-inertia-react.tape` | `vukem new app --inertia react`, `ls` and the generated `resources/js` pages |
| `tests.gif` | `tapes/tests.tape` | `go test -count=1` over core packages, one `ok <pkg> <time>` line each and a pass summary |
| `seeds-migrations.gif` | `tapes/seeds-migrations.tape` | `make:model -m -f`, `make:seeder` (plain and `--app`), `migrate`, `db:seed` |
| `scaffold.gif` | `tapes/scaffold.tape` | `make:controller --resource`, `make:request`, the resource route and `route:list` |

## Layout rules

The font is 32 px on a 1440x720 canvas, so the terminal has about 72 columns and 16 rows. Keep every screen inside that: clear with `Ctrl+L` between steps, trim output with `grep`, `head` or `cut`, and sleep 2-3 s per screen. Real output only; trimming is visible in the typed command.

- The first command is typed while hidden and `Show` runs right after, so frame 1 already shows it.
- Slow commands run under `Hide` so the GIF has no dead time. `Wait` can return early on a still-running command, so `vukem new` waits use a fixed `Sleep` while hidden.
- `vukem new` prints `go get` lines (with the framework version) on stderr; the tapes always send it to `/dev/null` so no version shows on screen.
- `tests.tape` defines hidden helpers: `P` (the package list) and `short` (reduces `go test` output to `ok <pkg> <time>` plus a summary).

## Regenerate

Needs Docker and Go. From the repository root:

```sh
docs/media/record.sh              # all GIFs
docs/media/record.sh new-htmx     # one tape
```

The script builds `bin/vukem` for Linux, builds the `vukem-vhs:local` image from `Dockerfile` (VHS `v0.11.0` plus Go `1.27.3`, both pinned), and runs each tape with the repository mounted at `/src`. Go module and build caches live in the `vukem-vhs-gomod` and `vukem-vhs-gobuild` Docker volumes, so only the first run is slow.

`tapes/warm.sh` runs hidden before each recording to fill those caches, which keeps `vukem new` to a few seconds. The Inertia and HTMX starters use `--no-install`, so no Node.js is needed.

To check that no version string is on screen, add `Output check.txt` to a tape, record it, run `grep 'v0\.' check.txt`, then delete the dump.
