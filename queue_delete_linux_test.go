//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func removalSnapshot(t *testing.T, path string) *outputRemoval {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id, _, err := identifyPartial(f)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return &outputRemoval{Path: path, Device: id.Device, Inode: id.Inode, Size: info.Size(), Modified: info.ModTime().UnixNano()}
}

func TestDeleteCompletedFileChecksConfirmationAndOwnership(t *testing.T) {
	for _, name := range []string{"delete", "missing", "replaced", "modified", "symlink", "directory", "no confirmation", "wrong path", "locked", "saved partial", "shared output", "replacement output"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dest := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(dest, []byte("downloaded"), 0600); err != nil {
				t.Fatal(err)
			}
			expected := removalSnapshot(t, dest)
			q := queueData{Version: 2, NextID: 2, Jobs: []queueJob{{ID: 1, URL: "https://example.com/file", Destination: dest, State: "completed"}}}
			qp, err := queuePath()
			if err != nil {
				t.Fatal(err)
			}
			cp, err := checkpointLocation(dest)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "missing":
				if err := os.Remove(dest); err != nil {
					t.Fatal(err)
				}
				expected = nil
			case "replaced", "symlink", "directory":
				if err := os.Rename(dest, dest+".original"); err != nil {
					t.Fatal(err)
				}
				if name == "replaced" {
					err = os.WriteFile(dest, []byte("different!"), 0600)
				}
				if name == "symlink" {
					err = os.Symlink(dest+".original", dest)
				}
				if name == "directory" {
					err = os.Mkdir(dest, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "modified":
				if err := os.WriteFile(dest, []byte("modified content"), 0600); err != nil {
					t.Fatal(err)
				}
			case "no confirmation":
				expected = nil
			case "wrong path":
				expected.Path += ".other"
			case "locked":
				lock, err := lockJobRecord(cp + ".lock")
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			case "saved partial":
				if err := os.WriteFile(dest+".part", []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "shared output":
				q.NextID++
				q.Jobs = append(q.Jobs, queueJob{ID: 2, URL: "https://example.com/other", Destination: dest + ".other", OutputPath: dest, State: "completed"})
			case "replacement output":
				q.Jobs[0].OutputPath = dest
				q.Jobs[0].Destination = dest + ".staged"
			}
			_, err = deleteQueueFiles(&q, 0, qp, expected)
			wantSuccess := name == "delete" || name == "missing" || name == "replacement output"
			if (err == nil) != wantSuccess {
				t.Fatalf("delete error=%v; success wanted=%v", err, wantSuccess)
			}
			_, statErr := os.Lstat(dest)
			if wantSuccess && !os.IsNotExist(statErr) {
				t.Fatalf("output not removed: %v", statErr)
			}
			if !wantSuccess && statErr != nil {
				t.Fatalf("refused file was removed: %v", statErr)
			}
		})
	}
}

func TestDeleteQueueFilesUsesVerifiedPartialCleanup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	q, qp, cp, dest, _ := ownedCleanupFixture(t)
	if fatal, err := deleteQueueFiles(&q, 0, qp, nil); fatal || err != nil {
		t.Fatalf("cleanup: fatal=%v error=%v", fatal, err)
	}
	for _, p := range []string{cp, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("saved data remains at %s: %v", p, err)
		}
	}
	if q.Jobs[0].State != "canceled" {
		t.Fatal("cleanup did not finish")
	}
}
