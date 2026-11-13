package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Parsania80/FluxGet/internal/downloadcategory"
)

func TestUnifiedSortedServerNameSurvivesReopenAndConflict(t *testing.T) {
	setTestConfig(t, t.TempDir())
	binary := unifiedCLI(t)
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="server.pdf"`)
		_, _ = w.Write([]byte("document contents"))
	}))
	defer server.Close()
	q := unifiedQueue(t, binary, 1)
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	initial, err := downloadcategory.Destination(dir, "fallback.mp3")
	if err != nil {
		t.Fatal(err)
	}
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: initial, AutomaticName: true, SortFiles: true, State: "paused"}})
	if err := q.send("quit"); err != nil {
		t.Fatal(err)
	}
	<-q.done
	q = unifiedQueue(t, binary, 1)
	entries := unifiedRequest(t, q, desktopRequest{Operation: "snapshot"}).Jobs
	if len(entries) != 1 || !entries[0].SortFiles || !entries[0].AutomaticName || entries[0].State != "paused" {
		t.Fatalf("sorting choice lost across reopen: %+v", entries)
	}
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	if err := q.send("resume 1"); err != nil {
		t.Fatal(err)
	}
	entries = unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "completed" })
	final := filepath.Join(dir, "Documents", "server.pdf")
	if entries[0].Destination != final || entries[0].SortFiles || entries[0].AutomaticName {
		t.Fatalf("final name/category not committed: %+v", entries[0])
	}
	if data, err := os.ReadFile(final); err != nil || string(data) != "document contents" {
		t.Fatal("wrong categorized output", err)
	}
	if _, err := os.Lstat(initial); !os.IsNotExist(err) {
		t.Fatal("URL category retained an output", err)
	}
	initial, err = downloadcategory.Destination(dir, "another.zip")
	if err != nil {
		t.Fatal(err)
	}
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: initial, AutomaticName: true, SortFiles: true}})
	entries = unifiedWait(t, q, func(j []queueEntry) bool { return len(j) == 2 && j[1].State == "failed" })
	if entries[1].Conflict != final {
		t.Fatalf("categorized conflict not surfaced: %+v", entries[1])
	}
	copy := filepath.Join(dir, "Documents", "copy.pdf")
	unifiedRequest(t, q, desktopRequest{Operation: "retarget", Job: queueEntry{ID: 2, Destination: copy}})
	if err := q.send("resume 2"); err != nil {
		t.Fatal(err)
	}
	entries = unifiedWait(t, q, func(j []queueEntry) bool { return j[1].State == "completed" })
	if entries[1].Destination != copy || entries[1].SortFiles {
		t.Fatal("explicit conflict resolution was rerouted")
	}
	if data, err := os.ReadFile(final); err != nil || string(data) != "document contents" {
		t.Fatal("existing categorized output changed", err)
	}
}
