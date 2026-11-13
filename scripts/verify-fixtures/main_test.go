package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
)

func fixtures(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range mock.Names() {
		f, err := mock.Generate(name, mock.DefaultSeed)
		if err != nil {
			t.Fatal(err)
		}
		data, err := mock.Encode(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestValidFixturesExerciseBothRuntimeScenarios(t *testing.T) {
	var out bytes.Buffer
	if err := verify(fixtures(t), &out); err != nil {
		t.Fatal(err)
	}
	for _, name := range mock.Names() {
		if !strings.Contains(out.String(), "PASS "+name) {
			t.Fatalf("missing evidence for %s: %s", name, &out)
		}
	}
}

func TestRejectsMalformedOrStaleInputWithUsefulDiagnostic(t *testing.T) {
	for _, tc := range []struct{ name, before, after, want string }{
		{"real mode", `"mode": "mock"`, `"mode": "real"`, "payload.mode"},
		{"not fictional", `"fictional": true`, `"fictional": false`, "fictional"},
		{"sequence gap", `"seq": 1,`, `"seq": 2,`, "seq"},
		{"unknown field", `"fictional": true,`, `"unexpected": true, "fictional": true,`, "unknown field"},
		{"trailing JSON", "", "", "unexpected data"},
		{"stale seed", `"seed": 4330`, `"seed": 4331`, "differs from seed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixtures(t)
			path := filepath.Join(dir, mock.RestaurantTimeConflict+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(data), tc.before, tc.after, 1)
			if tc.name == "trailing JSON" {
				changed = string(data) + "{}"
			}
			if changed == string(data) {
				t.Fatal("test did not change fixture")
			}
			if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			err = verify(dir, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), filepath.Base(path)) {
				t.Fatalf("error = %v; want filename and %q", err, tc.want)
			}
		})
	}
}

func TestMissingAndUnexpectedFixturesFail(t *testing.T) {
	for _, extra := range []bool{false, true} {
		dir := fixtures(t)
		var err error
		if extra {
			err = os.WriteFile(filepath.Join(dir, "unexpected.json"), []byte("{}"), 0o600)
		} else {
			err = os.Remove(filepath.Join(dir, mock.RestaurantTimeConflict+".json"))
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := verify(dir, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "expected exactly") {
			t.Fatalf("error = %v", err)
		}
	}
}
