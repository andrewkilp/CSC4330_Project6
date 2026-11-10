# Mock scenarios (#5)

`generate.sh` rebuilds the fixtures in `docs/fixtures` from `packages/go/mock`. It runs offline and never places a call.

| Command | Result |
| --- | --- |
| `scripts/scenarios/generate.sh` | Rewrite both fixtures with the default seed (4330) |
| `scripts/scenarios/generate.sh --check` | Exit 1 and name each stale fixture instead of writing |
| `scripts/scenarios/generate.sh --seed=7` | Write a variant with different IDs and timing; do not commit non-default seeds |

On Windows without bash, run the same command directly: `go run ./packages/go/mock/cmd/scenarios -out docs/fixtures [-check] [-seed N]`.

`moon run mock:test` also fails if a committed fixture differs from the generator.
