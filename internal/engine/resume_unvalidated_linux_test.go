//go:build linux

package engine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestResumeWithoutValidatorVerifiesPrefix(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "changed"}[changed], func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := []byte("original content from server")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "28")
				if requests.Add(1) == 1 {
					_, _ = w.Write(payload[:8])
					return
				}
				if r.Header.Get("Range") != "" {
					t.Error("validator-free verification must use one full response")
				}
				if changed {
					_, _ = w.Write([]byte("changed! content from server"))
					return
				}
				_, _ = w.Write(payload)
			}))
			defer server.Close()
			dest := filepath.Join(t.TempDir(), "file")
			if _, err := download(context.Background(), server.Client(), server.URL, dest, "", io.Discard); err == nil {
				t.Fatal("expected interruption")
			}
			path, err := checkpointLocation(dest)
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := loadCheckpoint(path)
			if err != nil {
				t.Fatal(err)
			}
			legacy.Total = nil
			partial, err := os.OpenFile(dest+".part", os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := saveCheckpoint(path, partial, legacy); err != nil {
				partial.Close()
				t.Fatal(err)
			}
			if err := partial.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = resumeDownloadWithLimit(context.Background(), server.Client(), dest, "", io.Discard, 1)
			if requests.Load() != 2 {
				t.Fatalf("got %d requests", requests.Load())
			}
			if changed {
				if err == nil {
					t.Fatal("changed prefix was accepted")
				}
				data, e := os.ReadFile(dest + ".part")
				if e != nil || !bytes.Equal(data, payload[:8]) {
					t.Fatalf("partial changed: %q, %v", data, e)
				}
				if _, e := os.Stat(dest); !os.IsNotExist(e) {
					t.Fatal("unexpected final file")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				data, e := os.ReadFile(dest)
				if e != nil || !bytes.Equal(data, payload) {
					t.Fatalf("final file: %q, %v", data, e)
				}
			}
		})
	}
}
