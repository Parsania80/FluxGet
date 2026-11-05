//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestHistoryRecoveryAndReplacement(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	original := filepath.Join(dir, "example.txt")
	if err := os.WriteFile(original, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := replacementFor(original)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := copyPath(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copy, []byte("new content"), 0o600); err != nil {
		t.Fatal(err)
	}
	jobs := []job{{URL: "https://example.com/file", Path: copy, State: "Downloading", Replace: identity}}
	if err := saveHistory(jobs); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadHistory()
	if err != nil || len(loaded) != 1 || loaded[0].State != "Interrupted" || loaded[0].Replace == nil {
		t.Fatalf("history: %+v, %v", loaded, err)
	}
	if err := completeReplacement(&loaded[0]); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "new content" || loaded[0].Path != original || loaded[0].Replace != nil {
		t.Fatalf("replacement %q, %+v, %v", data, loaded[0], err)
	}
}

func TestReplacementPreservesChangedOriginal(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "file")
	staged := filepath.Join(dir, "file (1)")
	if err := os.WriteFile(original, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := replacementFor(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := job{Path: staged, Replace: identity}
	if err := completeReplacement(&j); err == nil {
		t.Fatal("changed original was replaced")
	}
	for path, expected := range map[string]string{original: "changed", staged: "new"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("%s: %q, %v", path, data, err)
		}
	}
}

func TestCopyNameSkipsExistingFilesAndPartials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	for _, name := range []string{"report (1).txt", "report (2).txt.part"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := copyPath(path)
	if err != nil || got != filepath.Join(dir, "report (3).txt") {
		t.Fatalf("copy path: %q, %v", got, err)
	}
}

func TestDesktopHistoryIsExclusivelyOwned(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first, err := lockHistory()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := lockHistory()
	if err == nil {
		second.Close()
		t.Fatal("second desktop instance acquired history lock")
	}
}

func TestDiscoverInterruptedCheckpoints(t *testing.T) {
	known := filepath.Join(t.TempDir(), "known")
	unknown := filepath.Join(t.TempDir(), "new file")
	existing := []job{{Path: known, State: "Paused"}}
	output := "interrupted\t10/100 bytes\t" + strconv.Quote(known) + "\nready-to-reconcile\t100/100 bytes\t" + strconv.Quote(unknown) + "\nactive\t0/100 bytes\t\"/tmp/active\"\n"
	merged := mergeSavedJobs(existing, output)
	if len(merged) != 2 || merged[1].Path != unknown || merged[1].State != "Interrupted" {
		t.Fatalf("merged: %+v", merged)
	}
}
