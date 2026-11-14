//go:build linux

package engine

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func browserRequest(t *testing.T) desktopRequest {
	t.Helper()
	return desktopRequest{Operation: "browser-prepare", Action: "capture", Job: queueJob{URL: "https://example.org/file.iso", Destination: filepath.Join(t.TempDir(), "file.iso"), IntakeID: "chrome:profile-1234567890:request-1234567890"}}
}
func TestBrowserReceiptSurvivesRestartAndListRemoval(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, _ := queuePath()
	q := queueData{Version: 2, NextID: 1}
	if saveQueue(path, q) != nil {
		t.Fatal("fixture")
	}
	before, _ := os.ReadFile(path)
	req := browserRequest(t)
	req.Job.URL += "?token=persist%2Bexact&part=1&part=2"
	now := time.Now()
	id, changed, err := browserQueueOperation(&q, path, req, now)
	if err != nil || !changed || id != 1 || q.Jobs[0].State != "paused" || !pendingBrowserJob(q, q.Jobs[0]) {
		t.Fatal(q, err)
	}
	backup, _ := os.ReadFile(path + ".before-browser")
	if string(backup) != string(before) {
		t.Fatal("missing prior queue backup")
	}
	if err = saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	q, err = loadQueue(path)
	if err != nil || q.Version != 3 || q.Jobs[0].URL != req.Job.URL {
		t.Fatal(err)
	}
	if id2, changed, e := browserQueueOperation(&q, path, req, now); e != nil || changed || id2 != id || len(q.Jobs) != 1 {
		t.Fatal("duplicate job on replay", e)
	}
	bad := req
	bad.Job.URL += "&changed=1"
	if _, _, e := browserQueueOperation(&q, path, bad, now); e == nil {
		t.Fatal("ID reused")
	}
	activate := req
	activate.Operation = "browser-activate"
	activate.Job.State = "queued"
	if _, _, e := browserQueueOperation(&q, path, activate, now); e == nil {
		t.Fatal("started before cancellation confirmed")
	}
	activate.Action = "browser-canceled"
	if _, _, e := browserQueueOperation(&q, path, activate, now); e != nil || q.Jobs[0].State != "queued" {
		t.Fatal(e)
	}
	q.Jobs[0].State = "paused"
	if _, changed, e := browserQueueOperation(&q, path, activate, now); e != nil || changed || q.Jobs[0].State != "paused" {
		t.Fatal("replay resumed a user-paused job")
	}
	q.Jobs = nil
	if saveQueue(path, q) != nil {
		t.Fatal("save removed history")
	}
	q, err = loadQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, e := browserQueueOperation(&q, path, req, now); e == nil || len(q.Jobs) != 0 {
		t.Fatal("removed job recreated")
	}
}
func TestBrowserPendingAbortExpiryAndLater(t *testing.T) {
	for _, kind := range []string{"abort", "expired", "later", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			path, _ := queuePath()
			q := queueData{Version: 2, NextID: 1}
			_ = saveQueue(path, q)
			req := browserRequest(t)
			req.BrowserLater = kind == "later"
			now := time.Now()
			if _, _, e := browserQueueOperation(&q, path, req, now); e != nil {
				t.Fatal(e)
			}
			req.Operation = "browser-activate"
			req.Action = "browser-canceled"
			req.Job.State = "queued"
			if kind == "expired" {
				now = now.Add(61 * time.Second)
			}
			if kind == "disabled" {
				req.Job.State = "paused"
			}
			if kind == "abort" {
				req.Operation = "browser-abort"
			}
			if _, _, e := browserQueueOperation(&q, path, req, now); e != nil {
				t.Fatal(e)
			}
			if q.Jobs[0].State == "queued" {
				t.Fatal("unexpected start", kind)
			}
			if kind == "abort" {
				req.Operation = "browser-activate"
				if _, _, e := browserQueueOperation(&q, path, req, now); e == nil {
					t.Fatal("aborted capture started")
				}
			}
		})
	}
}
func TestBrowserQueuePrepareDoesNotDownloadBeforeConfirmation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newAutomaticHTTPFixture(t)
	h := startAutomaticQueueHarness(t, func(at time.Time) hardwareSnapshot { return knownHardware(at, 20) })
	req := browserRequest(t)
	req.Job.URL = s.server.URL
	prepared := h.request(t, req)
	if prepared.BrowserStage != "pending" || prepared.Jobs[0].State != "paused" {
		t.Fatal("not pending")
	}
	for i := 0; i < 25; i++ {
		h.ticks <- time.Unix(0, h.at.Add(int64(time.Second)))
		h.request(t, desktopRequest{Operation: "snapshot"})
	}
	if s.probes.Load() != 0 || s.count() != 0 {
		t.Fatal("network before handoff confirmation")
	}
	// Neither the desktop Resume path nor the CLI Resume command may start it.
	if _, err := h.commands.Write([]byte("resume 1\n")); err != nil {
		t.Fatal(err)
	}
	h.request(t, desktopRequest{Operation: "snapshot"})
	if s.count() != 0 {
		t.Fatal("manual resume bypassed pending guard")
	}
	req.Operation = "browser-activate"
	req.Action = "browser-canceled"
	req.Job.State = "queued"
	h.request(t, req)
	waitQueue(t, func() bool { return s.count() == 1 })
	close(s.finish)
	waitQueue(t, func() bool { return h.request(t, desktopRequest{Operation: "snapshot"}).Jobs[0].State == "completed" })
	requireContents(t, req.Job.Destination, s.payload)
	raw, _ := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fluxget", "queue.json"))
	if !strings.Contains(string(raw), `"version":3`) {
		t.Fatal("receipt schema not versioned")
	}
}

func TestBrowserRemovedPendingDestinationCannotBeConfirmed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, _ := queuePath()
	q := queueData{Version: 2, NextID: 1}
	_ = saveQueue(path, q)
	req := browserRequest(t)
	if _, _, e := browserQueueOperation(&q, path, req, time.Now()); e != nil {
		t.Fatal(e)
	}
	q.Jobs = nil
	req.Operation = "browser-status"
	if _, _, e := browserQueueOperation(&q, path, req, time.Now()); e == nil {
		t.Fatal("removed destination reported ready")
	}
	req.Operation = "browser-abort"
	if _, _, e := browserQueueOperation(&q, path, req, time.Now()); e != nil {
		t.Fatal(e)
	}
	if q.BrowserReceipts[req.Job.IntakeID].Stage != "aborted" {
		t.Fatal("receipt lost")
	}
}

func TestBrowserFailureRedactsQueryFromMessagesAndLogs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	h := startAutomaticQueueHarness(t)
	req := browserRequest(t)
	req.Job.URL = server.URL + "/file.iso?token=not-for-diagnostics"
	h.request(t, req)
	req.Operation, req.Action, req.Job.State = "browser-activate", "browser-canceled", "queued"
	h.request(t, req)
	var message string
	waitQueue(t, func() bool {
		jobs := h.request(t, desktopRequest{Operation: "snapshot"}).Jobs
		if jobs[0].State != "failed" {
			return false
		}
		message = jobs[0].Message
		return true
	})
	if strings.Contains(message, "not-for-diagnostics") || !strings.Contains(message, "HTTP 403") {
		t.Fatal("browser failure message did not redact the source link")
	}
	h.output.mu.Lock()
	defer h.output.mu.Unlock()
	for _, line := range strings.Split(h.output.text, "\n") {
		// Structured snapshots intentionally transport the source URL privately
		// to the desktop. Human-readable failure diagnostics must not repeat it.
		if !strings.HasPrefix(line, "DESKTOP_JSON ") && strings.Contains(line, "not-for-diagnostics") {
			t.Fatal("browser URL leaked into a failure log")
		}
	}
}
