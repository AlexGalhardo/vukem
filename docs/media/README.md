# Terminal recordings

GIFs embedded in the main README, recorded with [VHS](https://github.com/charmbracelet/vhs) from the tapes in `tapes/`.

| GIF | Tape | Shows |
| --- | --- | --- |
| `new-api.gif` | `tapes/new-api.tape` | `vukem new shop --api`, `vukem migrate`, `vukem serve`, `curl /api/v1/posts` |
| `new-htmx.gif` | `tapes/new-htmx.tape` | `vukem new site` (HTMX starter) and `vukem route:list` |
| `new-inertia-react.gif` | `tapes/new-inertia-react.tape` | `vukem new app --inertia react` and the generated pages |
| `tests.gif` | `tapes/tests.tape` | `go test -count=1` over core packages |
| `seeds-migrations.gif` | `tapes/seeds-migrations.tape` | `make:model -m -f`, `make:seeder` (plain and `--app`), `migrate`, `db:seed` |
| `scaffold.gif` | `tapes/scaffold.tape` | `make:controller --resource`, `make:request`, `route:list` |

## Regenerate

Needs Docker and Go. From the repository root:

```sh
docs/media/record.sh              # all GIFs
docs/media/record.sh new-htmx     # one tape
```

The script builds `bin/vukem` for Linux, builds the `vukem-vhs:local` image from `Dockerfile` (VHS `v0.11.0` plus Go `1.27.3`, both pinned), and runs each tape with the repository mounted at `/src`. Go module and build caches live in the `vukem-vhs-gomod` and `vukem-vhs-gobuild` Docker volumes, so only the first run is slow.

`tapes/warm.sh` runs hidden before each recording to fill those caches, which keeps `vukem new` to a few seconds on screen. The Inertia and HTMX starters use `--no-install`, so no Node.js is needed.
