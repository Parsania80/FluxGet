package downloadcategory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCategoriesAndSafeFolders(t *testing.T) {
	base := t.TempDir()
	for _, tc := range []struct{ name, folder string }{
		{"song.MP3", "Music"}, {"audio.opus", "Music"}, {"clip.mp4", "Videos"},
		{"photo.JPEG", "Pictures"}, {"report.pdf", "Documents"}, {"table.xlsx", "Documents"},
		{"backup.tar.gz", "Archives"}, {"setup.AppImage", "Programs"},
		{"download", "Other"}, {"unknown.bin", "Other"}, {"song.mp3.exe", "Programs"},
	} {
		path, err := Destination(base, tc.name)
		if err != nil || path != filepath.Join(base, tc.folder, tc.name) {
			t.Fatalf("%s: %q, %v", tc.name, path, err)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape.mp3", `..\escape.mp3`} {
		if _, err := Destination(base, name); err == nil {
			t.Fatalf("unsafe filename accepted: %q", name)
		}
	}
	if _, err := Destination(filepath.Join(base, "missing"), "song.mp3"); err == nil {
		t.Fatal("missing base folder accepted")
	}
}

func TestCategoryRefusesFilesAndLinks(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "Music")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Destination(base, "song.mp3"); err == nil {
		t.Fatal("category file accepted")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatal("existing category file changed", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "Documents")); err != nil {
		t.Skip("symlink creation unavailable:", err)
	}
	if _, err := Destination(base, "report.pdf"); err == nil {
		t.Fatal("category link accepted")
	}
	if files, err := os.ReadDir(outside); err != nil || len(files) != 0 {
		t.Fatal("category link wrote outside the base", err)
	}
}
