package engine

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/networkproxy"
)

func TestQueueProxyOwnsProbesRedirectsAndParallelRanges(t *testing.T) {
	setConfigForProxyTest(t)
	payload := bytes.Repeat([]byte("proxy fixture"), 100000)
	var requests, ranges, redirects atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/start" {
			redirects.Add(1)
			http.Redirect(w, r, "http://other.invalid/file", 302)
			return
		}
		if r.Header.Get("Range") != "" {
			ranges.Add(1)
		}
		w.Header().Set("ETag", `"proxy-fixture"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer proxy.Close()
	job := queueJob{ID: 1, URL: "http://source.invalid/start", Destination: filepath.Join(t.TempDir(), "payload.bin"), ExpectedSHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Proxy: &networkproxy.Config{Enabled: true, URL: proxy.URL}}
	if err := runQueueDownloadWithProgress(t.Context(), job, 4, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(job.Destination)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("proxied output changed", err)
	}
	if requests.Load() < 5 || ranges.Load() < 4 || redirects.Load() == 0 {
		t.Fatalf("missing proxied probe/ranges/redirects: %d/%d/%d", requests.Load(), ranges.Load(), redirects.Load())
	}
}

func TestProxyRoutePersistenceAndPauseRequirement(t *testing.T) {
	setConfigForProxyTest(t)
	path, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	q := queueData{Version: 2, NextID: 2, Jobs: []queueJob{{ID: 1, URL: "https://example.com/file", Destination: filepath.Join(t.TempDir(), "file"), State: "paused"}}}
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	proxy := &networkproxy.Config{Enabled: true, URL: "socks5://127.0.0.1:10808"}
	if err := setQueueProxy(&q, path, 1, proxy); err != nil {
		t.Fatal(err)
	}
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadQueue(path)
	if err != nil || loaded.Version != 4 || loaded.Jobs[0].Proxy == nil || loaded.Jobs[0].Proxy.URL != "socks5h://127.0.0.1:10808" {
		t.Fatal("proxy route did not survive restart", err)
	}
	backup, _ := os.ReadFile(path + ".before-proxy")
	if !bytes.Equal(backup, before) {
		t.Fatal("pre-proxy backup was not preserved")
	}
	loaded.Jobs[0].State = "running"
	if err := setQueueProxy(&loaded, path, 1, nil); err == nil || loaded.Jobs[0].Proxy == nil {
		t.Fatal("changed route of running transfer")
	}
	loaded.Jobs[0].State = "paused"
	if err := setQueueProxy(&loaded, path, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := saveQueue(path, loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = loadQueue(path)
	if err != nil || loaded.Version != 4 || loaded.Jobs[0].Proxy != nil {
		t.Fatal("Direct route was not retained", err)
	}
	validFile, _ := os.ReadFile(path)
	loaded.Jobs[0].Proxy = &networkproxy.Config{Enabled: true, URL: "http://user:private-token@host:80"}
	if err := saveQueue(path, loaded); err == nil {
		t.Fatal("saved rejected proxy credentials")
	}
	stillValid, _ := os.ReadFile(path)
	if !bytes.Equal(stillValid, validFile) {
		t.Fatal("invalid proxy replaced previous queue")
	}
}

func TestStandaloneCLIProxyAndRejectedCredentials(t *testing.T) {
	setConfigForProxyTest(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "proxy bytes") }))
	defer proxy.Close()
	destination := filepath.Join(t.TempDir(), "file")
	var out, stderr bytes.Buffer
	if code := Run([]string{"-url", "http://source.invalid/file", "-proxy", proxy.URL, "-out", destination, "-connections", "1"}, &out, &stderr); code != 0 {
		t.Fatalf("CLI proxy failed: %s", stderr.String())
	}
	data, _ := os.ReadFile(destination)
	if string(data) != "proxy bytes" {
		t.Fatal("CLI ignored proxy")
	}
	stderr.Reset()
	if code := Run([]string{"-url", "https://example.com/file", "-proxy", "http://user:private-token@host:80"}, &out, &stderr); code != 2 || bytes.Contains(stderr.Bytes(), []byte("private-token")) {
		t.Fatal("invalid proxy accepted or credentials exposed")
	}
}

func TestBrowserProxyReceiptKeepsSchemaFour(t *testing.T) {
	setConfigForProxyTest(t)
	path, _ := queuePath()
	q := queueData{Version: 2, NextID: 1}
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	req := desktopRequest{Operation: "browser-prepare", Action: "capture", Job: queueJob{URL: "https://example.org/file", Destination: filepath.Join(t.TempDir(), "file"), IntakeID: "chrome:profile-1234567890:request-1234567890", Proxy: &networkproxy.Config{Enabled: true, URL: "http://127.0.0.1:10809"}}}
	if _, _, err := browserQueueOperation(&q, path, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadQueue(path)
	if err != nil || loaded.Version != 4 || loaded.Jobs[0].Proxy == nil || !pendingBrowserJob(loaded, loaded.Jobs[0]) {
		t.Fatal("browser receipt lost route or downgraded schema", err)
	}
}

func setConfigForProxyTest(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
}
