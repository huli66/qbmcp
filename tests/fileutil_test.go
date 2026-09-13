package tests

import (
	"os"
	"path/filepath"
	"testing"

	"qbmcp/internal/fileutil"
)

func TestAtomicJSONFailurePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := fileutil.WriteJSON(path, map[string]any{"state": "running"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := fileutil.WriteJSON(path, make(chan int)); err == nil {
		t.Fatal("accepted non-JSON data")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed write changed the original file: %s %v", after, err)
	}
	if err := fileutil.Write(path, []byte("stopped")); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(path)
	if err != nil || string(after) != "stopped" {
		t.Fatalf("replacement: %s %v", after, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v %v", entries, err)
	}
}
