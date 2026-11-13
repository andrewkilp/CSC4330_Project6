// Command verify-fixtures validates committed mock data and exercises the actual
// conversation loop up to its first approval boundary. It never constructs a
// real provider, starts a server, or replays fixture approval/audio events.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
)

func main() {
	dir := flag.String("fixtures", "docs/fixtures", "directory containing committed scenario JSON")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "verify-fixtures: unexpected arguments")
		os.Exit(1)
	}
	if err := verify(*dir, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "verify-fixtures:", err)
		os.Exit(1)
	}
}

func verify(dir string, out io.Writer) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	if len(paths) != len(mock.Names()) {
		return fmt.Errorf("%s: expected exactly %d scenario JSON files, found %d", dir, len(mock.Names()), len(paths))
	}
	for _, name := range mock.Names() {
		path := filepath.Join(dir, name+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		f, err := mock.Decode(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := mock.Validate(f); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		generated, err := mock.Generate(name, mock.DefaultSeed)
		if err != nil {
			return err
		}
		want, err := mock.Encode(generated)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, want) {
			return fmt.Errorf("%s: differs from seed %d generator; regenerate with go run ./packages/go/mock/cmd/scenarios", path, mock.DefaultSeed)
		}
		if err := smoke(name, generated); err != nil {
			return fmt.Errorf("%s: runtime mock smoke: %w", name, err)
		}
		fmt.Fprintf(out, "PASS %s: %d fictional fixture events; runtime reply matches script and stops for approval\n", name, len(f.Events))
	}
	fmt.Fprintln(out, "Offline mock verification passed. No calls, external AI, speech playback, UI integration or booking were tested.")
	return nil
}

func smoke(name string, f mock.Fixture) error {
	var expected mock.Response
	for _, e := range f.Events {
		if r, ok := e.Payload.(mock.Response); ok && r.Status == "suggested" {
			expected = r
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := mock.RunConversation(ctx, name, mock.DefaultSeed, &buf); err != nil {
		return err
	}
	dec := json.NewDecoder(&buf)
	var last conversation.ResponseEvent
	var count uint64
	for {
		var e conversation.ResponseEvent
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decode runtime event: %w", err)
		}
		count++
		if e.Type != "response.updated" || e.Seq != count || e.CallID != f.Events[0].CallID || e.Payload.CallID != e.CallID || e.Payload.ContextRevision != e.ContextRevision || !e.Payload.NeedsApproval {
			return fmt.Errorf("event %d: invalid response envelope or missing approval hold", count)
		}
		if e.Payload.Status != conversation.Generating && e.Payload.Status != conversation.Suggested {
			return fmt.Errorf("event %d: advanced beyond generation/approval boundary to %s", count, e.Payload.Status)
		}
		if count > 1 && (last.Payload.Status == conversation.Suggested || e.Payload.ID != last.Payload.ID) {
			return fmt.Errorf("event %d: generated another response after the approval boundary", count)
		}
		last = e
	}
	if count < 2 || last.Payload.Status != conversation.Suggested || last.Payload.Text != expected.Text || last.ContextRevision != expected.ContextRevision {
		return fmt.Errorf("expected a generated, approval-held suggestion matching the scenario")
	}
	return nil
}
