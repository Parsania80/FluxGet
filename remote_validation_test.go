package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestValidateRemote(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		etag         string
		contentRange string
		encoding     string
		body         string
		wantError    bool
	}{
		{"matching", 206, `"v1"`, "bytes 0-0/10", "", "x", false},
		{"changed tag", 206, `"v2"`, "bytes 0-0/10", "", "x", true},
		{"ignored range", 200, `"v1"`, "", "", "0123456789", true},
		{"wrong range", 206, `"v1"`, "bytes 1-1/10", "", "x", true},
		{"changed size", 206, `"v1"`, "bytes 0-0/11", "", "x", true},
		{"missing validator", 206, "", "bytes 0-0/10", "", "x", true},
		{"encoded response", 206, `"v1"`, "bytes 0-0/10", "gzip", "x", true},
		{"short body", 206, `"v1"`, "bytes 0-0/10", "", "", true},
		{"long body", 206, `"v1"`, "bytes 0-0/10", "", "xy", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "bytes=0-0" || r.Header.Get("If-Range") != `"v1"` || r.Header.Get("Accept-Encoding") != "identity" {
					t.Errorf("unexpected request headers: %v", r.Header)
				}
				w.Header().Set("ETag", tc.etag)
				w.Header().Set("Content-Range", tc.contentRange)
				w.Header().Set("Content-Encoding", tc.encoding)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			total := int64(10)
			destination := filepath.Join(t.TempDir(), "file")
			c := checkpoint{Version: 1, URL: server.URL, ResolvedURL: server.URL, Destination: destination, Partial: destination + ".part", ETag: `"v1"`, Total: &total, Completed: 5}
			err := validateRemote(context.Background(), server.Client(), c)
			if (err != nil) != tc.wantError {
				t.Fatalf("got %v; want error %t", err, tc.wantError)
			}
		})
	}
}

func TestValidateRemoteRejectsUnsafeCheckpoint(t *testing.T) {
	total := int64(10)
	destination := filepath.Join(t.TempDir(), "file")
	c := checkpoint{Version: 1, URL: "https://example.com/file", ResolvedURL: "https://example.com/file", Destination: destination, Partial: destination + ".part", ETag: `"v1"`, Total: &total, Completed: 5}
	for _, tag := range []string{"", `W/"v1"`, "v1", "\"bad\nvalue\""} {
		invalid := c
		invalid.ETag = tag
		if err := validateRemote(context.Background(), &http.Client{}, invalid); err == nil {
			t.Fatalf("accepted unsafe ETag %q", tag)
		}
	}
	c.ResolvedURL = ""
	if err := validateRemote(context.Background(), &http.Client{}, c); err == nil {
		t.Fatal("accepted missing resolved URL")
	}
}

func TestValidateRemoteRejectsChangedResolvedURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Range", "bytes 0-0/10")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("x"))
	}))
	defer server.Close()
	total := int64(10)
	destination := filepath.Join(t.TempDir(), "file")
	c := checkpoint{Version: 1, URL: server.URL, ResolvedURL: server.URL + "/old", Destination: destination, Partial: destination + ".part", ETag: `"v1"`, Total: &total, Completed: 5}
	if err := validateRemote(context.Background(), server.Client(), c); err == nil {
		t.Fatal("accepted changed resolved URL")
	}
}

func TestValidateRemoteRespectsCancellation(t *testing.T) {
	total := int64(10)
	destination := filepath.Join(t.TempDir(), "file")
	c := checkpoint{Version: 1, URL: "https://example.com/file", ResolvedURL: "https://example.com/file", Destination: destination, Partial: destination + ".part", ETag: `"v1"`, Total: &total, Completed: 5}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateRemote(ctx, &http.Client{}, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want cancellation", err)
	}
}
