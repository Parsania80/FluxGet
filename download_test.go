package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

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

			written, err := download(context.Background(), server.Client(), server.URL, destination)
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

			written, err := download(context.Background(), server.Client(), server.URL, destination)
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

	written, err := download(context.Background(), server.Client(), server.URL, destination)
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

			_, err := download(context.Background(), server.Client(), server.URL, destination)
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

	_, err := download(context.Background(), server.Client(), server.URL, destination)
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
		written, err := download(ctx, server.Client(), server.URL, destination)
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
