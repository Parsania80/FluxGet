//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResumeInterruptedDownload(t *testing.T) {
	for _, mode := range []string{"success", "changed after probe", "edited partial", "wrong checksum"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := []byte("a real interrupted download")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("ETag", `"v1"`)
				switch r.Header.Get("Range") {
				case "":
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
					w.Write(payload[:6])
				case "bytes=0-0":
					if r.Header.Get("If-Range") != `"v1"` {
						t.Errorf("unexpected If-Range: %s", r.Header.Get("If-Range"))
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
					w.WriteHeader(http.StatusPartialContent)
					w.Write(payload[:1])
				case "bytes=6-":
					if mode == "changed after probe" {
						w.Header().Set("ETag", `"v2"`)
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 6-%d/%d", len(payload)-1, len(payload)))
						w.WriteHeader(http.StatusPartialContent)
						w.Write(payload[6:])
						return
					}
					if r.Header.Get("If-Range") != `"v1"` {
						t.Errorf("unexpected If-Range: %s", r.Header.Get("If-Range"))
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 6-%d/%d", len(payload)-1, len(payload)))
					w.WriteHeader(http.StatusPartialContent)
					w.Write(payload[6:])
				default:
					t.Errorf("unexpected range: %q", r.Header.Get("Range"))
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			destination := filepath.Join(t.TempDir(), "file")
			sum := sha256.Sum256(payload)
			expected := fmt.Sprintf("%x", sum)
			if mode == "wrong checksum" {
				expected = strings.Repeat("0", 64)
			}
			if _, err := download(context.Background(), server.Client(), server.URL, destination, expected, nil); err == nil {
				t.Fatal("expected interrupted download")
			}
			path, err := checkpointLocation(destination)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, err := loadCheckpoint(path)
			if err != nil || checkpoint.Completed != 6 || checkpoint.State != "interrupted" || checkpoint.ExpectedSHA256 == "" || checkpoint.PrefixSHA256 == "" {
				t.Fatalf("checkpoint: %+v, error %v", checkpoint, err)
			}
			if mode == "edited partial" {
				if err := os.WriteFile(destination+".part", []byte("edited"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "success" {
				partial, err := os.OpenFile(destination+".part", os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				lock, err := lockJob(path+".lock", partial)
				if err != nil {
					t.Fatal(err)
				}
				// A version-one checkpoint remains usable after upgrading FluxGet.
				checkpoint.Version = 1
				checkpoint.State = ""
				if err := saveCheckpoint(path, partial, checkpoint); err != nil {
					lock.Close()
					t.Fatal(err)
				}
				if _, err := resumeDownload(context.Background(), server.Client(), destination, "", nil); err == nil {
					t.Fatal("resumed an owned download")
				}
				lock.Close()
				partial.Close()
				var stdout, stderr bytes.Buffer
				if code := run([]string{"-resume", "-out", destination}, &stdout, &stderr); code != 0 {
					t.Fatalf("resume exited %d: %s", code, stderr.String())
				}
				requireContents(t, destination, payload)
				requireMissing(t, destination+".part")
				requireMissing(t, path)
				return
			}
			_, err = resumeDownload(context.Background(), server.Client(), destination, "", nil)
			if err == nil {
				t.Fatal("expected resume to fail")
			}
			requireMissing(t, destination)
			if mode == "changed after probe" {
				requireContents(t, destination+".part", payload[:6])
			} else if mode == "edited partial" {
				requireContents(t, destination+".part", []byte("edited"))
				if requests.Load() != 1 {
					t.Fatal("contacted server after local validation failed")
				}
			} else {
				requireContents(t, destination+".part", payload)
			}
			if _, err := loadCheckpoint(path); err != nil {
				t.Fatalf("checkpoint lost: %v", err)
			}
		})
	}
}

func TestCompletedDownloadRemovesPeriodicCheckpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), (1<<20)+65536)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"large"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload)
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	if _, err := download(context.Background(), server.Client(), server.URL, destination, "", nil); err != nil {
		t.Fatal(err)
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	requireContents(t, destination, payload)
	requireMissing(t, path)
}

func TestCheckpointWrittenDuringTransfer(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), (1<<20)+65536)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"large"`)
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)+1024))
		w.Write(payload)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := download(ctx, server.Client(), server.URL, destination, "", nil)
		done <- err
	}()
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		c, err := loadCheckpoint(path)
		if err == nil && c.Completed >= 1<<20 && c.State == "downloading" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("checkpoint not written during active transfer")
		case <-time.After(10 * time.Millisecond):
		case err := <-done:
			t.Fatalf("transfer ended before checkpoint: %v", err)
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected cancellation")
	}
	if c, err := loadCheckpoint(path); err != nil || c.State != "interrupted" {
		t.Fatalf("expected persisted interrupted state, got %+v, %v", c, err)
	}
}

func TestResumeAfterProcessCrash(t *testing.T) {
	if os.Getenv("FLUXGET_CRASH_TEST_CHILD") == "1" {
		_, err := download(context.Background(), &http.Client{}, os.Getenv("FLUXGET_CRASH_TEST_URL"), os.Getenv("FLUXGET_CRASH_TEST_DEST"), "", nil)
		t.Fatalf("download ended before simulated crash: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), 2<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"crash-test"`)
		if r.Header.Get("Range") != "" {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload[:(1<<20)+65536])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	child := exec.Command(os.Args[0], "-test.run=^TestResumeAfterProcessCrash$")
	child.Env = append(os.Environ(), "FLUXGET_CRASH_TEST_CHILD=1", "FLUXGET_CRASH_TEST_URL="+server.URL, "FLUXGET_CRASH_TEST_DEST="+destination)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		c, err := loadCheckpoint(path)
		if err == nil && c.Completed >= 1<<20 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("child did not save a checkpoint before crash")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	if _, err := resumeDownload(context.Background(), server.Client(), destination, "", nil); err != nil {
		t.Fatalf("resume after crash: %v", err)
	}
	requireContents(t, destination, payload)
	requireMissing(t, path)
}

func TestResumeCLIRequiresDestinationAndNoURL(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-resume", "-url", "https://example.com", "-out", "file"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "either -url") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}
