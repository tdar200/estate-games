package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestFixturesUpToDate regenerates the fixtures and compares them with the
// committed web/fixtures/poker, so engine changes that alter the state shape are
// noticed. Run `go run ./cmd/poker-fixtures` to refresh them.
func TestFixturesUpToDate(t *testing.T) {
	dir := t.TempDir()
	n := generate(dir, io.Discard)
	if n < 20 {
		t.Fatalf("only %d fixtures", n)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		want, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join("..", "..", "web", "fixtures", "poker", e.Name()))
		if err != nil {
			t.Fatalf("%s is missing from web/fixtures/poker: run go run ./cmd/poker-fixtures", e.Name())
		}
		if string(got) != string(want) {
			t.Errorf("web/fixtures/poker/%s is stale: run go run ./cmd/poker-fixtures", e.Name())
		}
	}
}
