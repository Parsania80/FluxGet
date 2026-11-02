package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDownloadChecksumAndProgress(t *testing.T) {
	payload := []byte("verified payload")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name      string
		checksum  string
		wantError bool
	}{
		{"matching", fmt.Sprintf("%X", sum), false},
		{"mismatch", strings.Repeat("0", 64), true},
		{"invalid", "bad", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "file")
			var progress bytes.Buffer
			_, err := download(context.Background(), server.Client(), server.URL, destination, tc.checksum, &progress)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error %t", err, tc.wantError)
			}
			if tc.name == "matching" {
				requireContents(t, destination, payload)
				if !strings.Contains(progress.String(), "100.0%") || !strings.Contains(progress.String(), "bytes/s") {
					t.Fatalf("missing progress: %q", progress.String())
				}
			} else {
				requireMissing(t, destination)
				if tc.name == "mismatch" {
					requireContents(t, destination+".part", payload)
				} else {
					requireMissing(t, destination+".part")
				}
			}
		})
	}
}

func TestDownloadUnknownLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		w.Write([]byte("stream"))
	}))
	t.Cleanup(server.Close)
	destination := filepath.Join(t.TempDir(), "file")
	var progress bytes.Buffer
	_, err := download(context.Background(), server.Client(), server.URL, destination, "", &progress)
	if err != nil {
		t.Fatal(err)
	}
	requireContents(t, destination, []byte("stream"))
	if !strings.Contains(progress.String(), "Downloaded 6 bytes") || strings.Contains(progress.String(), "%") {
		t.Fatalf("unexpected progress: %q", progress.String())
	}
}

func TestDownloadRedirectAndTLS(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secure"))
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	destination := filepath.Join(t.TempDir(), "file")
	if _, err := download(context.Background(), target.Client(), redirect.URL, destination, "", nil); err != nil {
		t.Fatal(err)
	}
	requireContents(t, destination, []byte("secure"))

	failedDestination := filepath.Join(t.TempDir(), "file")
	if _, err := download(context.Background(), &http.Client{}, target.URL, failedDestination, "", nil); err == nil {
		t.Fatal("expected an untrusted TLS certificate to fail")
	}
	requireMissing(t, failedDestination)
	requireMissing(t, failedDestination+".part")
}

func TestDownloadRejectsHTTPSDowngrade(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Write([]byte("insecure"))
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	destination := filepath.Join(t.TempDir(), "file")
	_, err := download(context.Background(), redirect.Client(), redirect.URL, destination, "", nil)
	if err == nil || !strings.Contains(err.Error(), "HTTPS to HTTP redirects are not allowed") {
		t.Fatalf("expected downgrade rejection, got %v", err)
	}
	if reached {
		t.Fatal("insecure redirect target was contacted")
	}
	requireMissing(t, destination)
	requireMissing(t, destination+".part")
}

func TestDownloadRedirectLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.String(), http.StatusFound)
	}))
	t.Cleanup(server.Close)
	destination := filepath.Join(t.TempDir(), "file")
	_, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("expected redirect limit, got %v", err)
	}
	requireMissing(t, destination)
	requireMissing(t, destination+".part")
}

func TestRunChecksum(t *testing.T) {
	payload := []byte("CLI checksum")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	t.Cleanup(server.Close)
	destination := filepath.Join(t.TempDir(), "file")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-url", server.URL, "-out", destination, "-sha256", fmt.Sprintf("%x", sum)}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d: %s", code, stderr.String())
	}
	requireContents(t, destination, payload)
	if !strings.Contains(stderr.String(), "100.0%") || !strings.Contains(stdout.String(), "Downloaded") {
		t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write(data []byte) (int, error) {
	return 1, io.ErrShortWrite
}

func TestProgressWriterReportsPartialWrite(t *testing.T) {
	tracker := &progressWriter{writer: failingWriter{}, start: time.Now()}
	n, err := tracker.Write([]byte("content"))
	if n != 1 || tracker.written != 1 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("got count %d, tracked %d, error %v", n, tracker.written, err)
	}
}

func TestDownloadSuccess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "binary", payload: bytes.Repeat([]byte{0, 1, 127, 128, 255}, 256)},
		{name: "empty", payload: []byte{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", strconv.Itoa(len(tc.payload)))
				w.Write(tc.payload)
			}))
			t.Cleanup(server.Close)
			destination := filepath.Join(t.TempDir(), "file.bin")

			written, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if written != int64(len(tc.payload)) {
				t.Fatalf("downloaded %d bytes, want %d", written, len(tc.payload))
			}
			requireContents(t, destination, tc.payload)
			requireMissing(t, destination+".part")
		})
	}
}

func TestDownloadRejectsHTTPFailure(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusServiceUnavailable, http.StatusPartialContent} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, "not a successful full-file response")
			}))
			t.Cleanup(server.Close)
			destination := filepath.Join(t.TempDir(), "file.bin")

			written, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
			if err == nil || written != 0 {
				t.Fatalf("got (%d, %v), want zero bytes and an error", written, err)
			}
			requireMissing(t, destination)
			requireMissing(t, destination+".part")
		})
	}
}

func TestDownloadPreservesTruncatedResponse(t *testing.T) {
	payload := []byte("only part of the promised response")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)+100))
		w.Write(payload)
	}))
	t.Cleanup(server.Close)
	destination := filepath.Join(t.TempDir(), "file.bin")

	written, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v, want an unexpected-EOF error", err)
	}
	if written != int64(len(payload)) {
		t.Fatalf("downloaded %d bytes, want %d", written, len(payload))
	}
	requireContents(t, destination+".part", payload)
	requireMissing(t, destination)
}

func TestDownloadProtectsExistingFiles(t *testing.T) {
	for _, suffix := range []string{"", ".part"} {
		t.Run("existing"+suffix, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, "new data")
			}))
			t.Cleanup(server.Close)
			destination := filepath.Join(t.TempDir(), "file.bin")
			protectedPath := destination + suffix
			original := []byte("keep these existing bytes")
			if err := os.WriteFile(protectedPath, original, 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
			if !errors.Is(err, os.ErrExist) {
				t.Fatalf("got %v, want an existing-file error", err)
			}
			requireContents(t, protectedPath, original)
			if suffix == ".part" {
				requireMissing(t, destination)
			} else {
				requireMissing(t, destination+".part")
			}
		})
	}
}

func TestDownloadProtectsDestinationCreatedDuringTransfer(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "file.bin")
	original := []byte("another writer owns this file")
	payload := []byte("downloaded content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := os.WriteFile(destination, original, 0o600); err != nil {
			t.Errorf("create competing destination: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(payload)
	}))
	t.Cleanup(server.Close)

	_, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v, want an existing-file error", err)
	}
	requireContents(t, destination, original)
	requireContents(t, destination+".part", payload)
}

func TestDownloadCancellationPreservesPartialData(t *testing.T) {
	payload := []byte("bytes received before cancellation")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)+100))
		w.Write(payload)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	destination := filepath.Join(t.TempDir(), "file.bin")
	type result struct {
		written int64
		err     error
	}
	done := make(chan result, 1)
	go func() {
		written, err := download(ctx, server.Client(), server.URL, destination, "", nil)
		done <- result{written: written, err: err}
	}()

	// Wait for a real write before canceling, not merely for response headers.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(destination + ".part")
		if err == nil && bytes.Equal(data, payload) {
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-timer.C:
			t.Fatal("download did not write its partial data")
		case <-ticker.C:
		case result := <-done:
			t.Fatalf("download ended before cancellation: %v", result.err)
		}
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("got %v, want a cancellation error", result.err)
		}
		if result.written != int64(len(payload)) {
			t.Fatalf("downloaded %d bytes, want %d", result.written, len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download did not stop after cancellation")
	}
	requireContents(t, destination+".part", payload)
	requireMissing(t, destination)
}

func requireContents(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s has unexpected contents: got %d bytes, want %d", path, len(got), len(want))
	}
}

func requireMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, got %v", path, err)
	}
}
