//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileKeepsUnverifiedFiles(t *testing.T) {
	for _, mode := range []string{"different inode", "modified partial", "modified final", "invalid digest", "legacy checkpoint", "wrong checksum"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			destination := filepath.Join(t.TempDir(), "file")
			payload := []byte("verified contents")
			if err := os.WriteFile(destination+".part", payload, 0o600); err != nil {
				t.Fatal(err)
			}
			partial, err := os.OpenFile(destination+".part", os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			path, err := checkpointLocation(destination)
			if err != nil {
				t.Fatal(err)
			}
			total := int64(len(payload))
			sum := sha256.Sum256(payload)
			c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name(), Total: &total, Completed: total, PrefixSHA256: fmt.Sprintf("%x", sum), State: "ready"}
			if mode == "invalid digest" {
				c.PrefixSHA256 = strings.Repeat("0", 64)
			}
			if mode == "legacy checkpoint" {
				c.State = ""
			}
			if err := saveCheckpoint(path, partial, c); err != nil {
				t.Fatal(err)
			}
			partial.Close()
			switch mode {
			case "different inode":
				if err := os.WriteFile(destination, payload, 0o600); err != nil {
					t.Fatal(err)
				}
			case "modified partial":
				if err := os.WriteFile(destination+".part", []byte("changed contents!"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "modified final":
				if err := os.Rename(destination+".part", destination); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, []byte("changed contents!"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "legacy checkpoint":
				if err := os.Link(destination+".part", destination); err != nil {
					t.Fatal(err)
				}
			}
			beforePartial, partialErr := os.ReadFile(destination + ".part")
			beforeFinal, finalErr := os.ReadFile(destination)
			beforeCheckpoint, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := ""
			if mode == "wrong checksum" {
				expected = strings.Repeat("0", 64)
			}
			if _, err := resumeDownload(context.Background(), &http.Client{}, destination, expected, nil); err == nil {
				t.Fatal("accepted unverified publication files")
			}
			afterPartial, afterPartialErr := os.ReadFile(destination + ".part")
			afterFinal, afterFinalErr := os.ReadFile(destination)
			afterCheckpoint, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(beforeCheckpoint, afterCheckpoint) || !bytes.Equal(beforePartial, afterPartial) || !bytes.Equal(beforeFinal, afterFinal) || (partialErr == nil) != (afterPartialErr == nil) || (finalErr == nil) != (afterFinalErr == nil) {
				t.Fatal("reconciliation changed files despite failed verification")
			}
		})
	}
}

func TestDownloadRecordsReadyStateForServerWithoutETag(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	destination := filepath.Join(t.TempDir(), "file")
	payload := []byte("complete download")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := os.WriteFile(destination, []byte("other file"), 0o600); err != nil {
			t.Error(err)
		}
		w.Write(payload)
	}))
	defer server.Close()
	if _, err := download(context.Background(), server.Client(), server.URL, destination, "", nil); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected destination conflict, got %v", err)
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadCheckpoint(path)
	if err != nil || c.State != "ready" || c.Completed != int64(len(payload)) {
		t.Fatalf("expected ready checkpoint, got %+v, %v", c, err)
	}
	if _, err := resumeDownload(context.Background(), server.Client(), destination, "", nil); err == nil {
		t.Fatal("reconciliation accepted a conflicting final file")
	}
	requireContents(t, destination, []byte("other file"))
	requireContents(t, destination+".part", payload)
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeDownload(context.Background(), server.Client(), destination, "", nil); err != nil {
		t.Fatalf("reconcile after conflict removed: %v", err)
	}
	requireContents(t, destination, payload)
	requireMissing(t, destination+".part")
	requireMissing(t, path)
}

func TestReconcileEmptyDownload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	destination := filepath.Join(t.TempDir(), "empty")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	sum := sha256.Sum256(nil)
	c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name(), Total: &total, PrefixSHA256: fmt.Sprintf("%x", sum), State: "ready"}
	if err := saveCheckpoint(path, partial, c); err != nil {
		t.Fatal(err)
	}
	partial.Close()
	if written, err := resumeDownload(context.Background(), &http.Client{}, destination, "", nil); err != nil || written != 0 {
		t.Fatalf("reconcile empty file = %d, %v", written, err)
	}
	requireContents(t, destination, []byte{})
	requireMissing(t, destination+".part")
	requireMissing(t, path)
}
