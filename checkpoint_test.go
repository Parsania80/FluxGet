package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { partial.Close() })
	if _, err := partial.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	total := int64(10)
	c := checkpoint{Version: 1, URL: "https://example.com/file", ResolvedURL: "https://cdn.example.com/file", Destination: destination, Partial: partial.Name(), ETag: `"version-1"`, Total: &total, Completed: 5}
	path := filepath.Join(dir, "job.json")
	if err := saveCheckpoint(path, partial, c); err != nil {
		t.Fatal(err)
	}
	got, err := loadCheckpoint(path)
	if err != nil || got.Version != c.Version || got.URL != c.URL || got.ResolvedURL != c.ResolvedURL || got.Destination != c.Destination || got.Partial != c.Partial || got.ETag != c.ETag || got.Total == nil || *got.Total != total || got.Completed != 5 {
		t.Fatalf("round trip: got %+v, error %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint permissions: %v, %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".fluxget-checkpoint-interrupted"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCheckpoint(path); err != nil {
		t.Fatalf("interrupted temporary update affected checkpoint: %v", err)
	}
}

func TestCheckpointRejectsInvalidInputAndPreservesPrevious(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { partial.Close() })
	if _, err := partial.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "job.json")
	c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name(), Completed: 5}
	if err := saveCheckpoint(path, partial, c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []checkpoint{
		{Version: 3, URL: c.URL, Destination: destination, Partial: c.Partial, Completed: 5},
		{Version: 1, URL: "https://user:pass@example.com/file", Destination: destination, Partial: c.Partial, Completed: 5},
		{Version: 1, URL: c.URL, Destination: destination, Partial: c.Partial, Completed: 6},
		{Version: 1, URL: c.URL + strings.Repeat("a", 1<<20), Destination: destination, Partial: c.Partial, Completed: 5},
	} {
		if err := saveCheckpoint(path, partial, invalid); err == nil {
			t.Fatalf("accepted invalid checkpoint: %+v", invalid)
		}
	}
	if err := saveCheckpoint(partial.Name(), partial, c); err == nil {
		t.Fatal("accepted partial file as checkpoint path")
	}
	if err := saveCheckpoint(destination, partial, c); err == nil {
		t.Fatal("accepted final file as checkpoint path")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("valid checkpoint changed: %v", err)
	}
	requireContents(t, partial.Name(), []byte("hello"))
}

func TestCheckpointFailedPublicationCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { partial.Close() })
	c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name()}
	target := filepath.Join(dir, "checkpoint-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveCheckpoint(target, partial, c); err == nil {
		t.Fatal("expected publication to an existing directory to fail")
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".fluxget-checkpoint-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary files remain: %v, error %v", temps, err)
	}
}

func TestLoadCheckpointRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.json")
	for _, content := range []string{"{", `{ "version": 2 }`, `{"version":1} {}`, `{"version":1,"unknown":true}`, `{"version":1,"url":"https://example.com","destination":"/tmp/file","partial":"/tmp/file.part","state":"unknown"}`, strings.Repeat(" ", (1<<20)+1)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCheckpoint(path); err == nil {
			t.Fatalf("accepted invalid checkpoint of length %d", len(content))
		}
	}
}
