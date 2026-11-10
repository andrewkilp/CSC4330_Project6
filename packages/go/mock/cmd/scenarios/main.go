// Command scenarios writes or checks the deterministic fixtures in docs/fixtures.
// It performs no network access and never starts a real call.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
)

func main() {
	seed := flag.Uint64("seed", mock.DefaultSeed, "seed for identifiers and timing jitter")
	out := flag.String("out", "docs/fixtures", "fixture directory")
	check := flag.Bool("check", false, "fail if committed fixtures differ instead of writing them")
	flag.Parse()
	if err := run(*seed, *out, *check); err != nil {
		fmt.Fprintln(os.Stderr, "scenarios:", err)
		os.Exit(1)
	}
}

func run(seed uint64, out string, check bool) error {
	stale := 0
	for _, name := range mock.Names() {
		f, err := mock.Generate(name, seed)
		if err != nil {
			return err
		}
		want, err := mock.Encode(f)
		if err != nil {
			return err
		}
		path := filepath.Join(out, name+".json")
		if check {
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				fmt.Fprintf(os.Stderr, "stale: %s (regenerate with scripts/scenarios/generate.sh)\n", path)
				stale++
				continue
			}
			fmt.Printf("ok: %s (%d events, seed %d)\n", path, len(f.Events), seed)
			continue
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d events, seed %d)\n", path, len(f.Events), seed)
	}
	if stale > 0 {
		return fmt.Errorf("%d fixture file(s) are out of date", stale)
	}
	return nil
}
