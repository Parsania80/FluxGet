package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveSpeedChangesWhileWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"default_kib":1,"overrides":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	limit := &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "download:1"}}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- limit.wait(context.Background(), 1024) }()
	time.Sleep(120 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":1,"overrides":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > 800*time.Millisecond {
			t.Fatal("updated limit took too long")
		}
	case <-time.After(time.Second):
		t.Fatal("disabling limit did not unblock transfer")
	}
}

func TestLiveSpeedDefaultAndIndividualCeilings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"default_kib":100,"overrides":{"queue:1":25,"queue:2":200}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		want int64
	}{{"queue:1", 25 * 1024}, {"queue:2", 100 * 1024}, {"queue:3", 100 * 1024}} {
		l := &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: tc.key}}
		if got := l.liveRate(); got != tc.want {
			t.Errorf("%s rate %d want %d", tc.key, got, tc.want)
		}
	}
}

func TestCLIAppliesLiveSpeedChange(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"default_kib":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 32*1024)) }))
	defer server.Close()
	done := make(chan int, 1)
	var out, stderr bytes.Buffer
	go func() {
		done <- Run([]string{"-url", server.URL, "-out", filepath.Join(t.TempDir(), "file"), "-connections", "1", "-live-speed-file", path, "-live-speed-key", "download:0"}, &out, &stderr)
	}()
	select {
	case <-done:
		t.Fatal("download finished despite enabled low limit")
	case <-time.After(400 * time.Millisecond):
	}
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("download failed: %s", stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download did not speed up after disabling limit")
	}
}

func TestQueueDownloadAppliesLiveSpeedChange(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":0,"overrides":{"queue:7":2}}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 32*1024)) }))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	ctx := context.WithValue(context.Background(), liveSpeedKey{}, path)
	done := make(chan error, 1)
	go func() {
		done <- runQueueDownloadWithProgress(ctx, queueJob{ID: 7, URL: server.URL, Destination: destination}, 1, io.Discard)
	}()
	select {
	case err := <-done:
		t.Fatalf("download finished despite low limit: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":0,"overrides":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queue download did not speed up")
	}
}

// Real timer scheduling must not turn a multi-MB/s ceiling into a ~1 MB/s cap.
func TestLiveSpeedSustainsMegabyteRate(t *testing.T) {
	for _, kib := range []int{2929, 11718} {
		t.Run(fmt.Sprint(kib), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "speed.json")
			if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"overrides":{"queue:1":%d}}`, kib)), 0600); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("x"), 6<<20)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			limitClient(client, &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "queue:1"}})
			start := time.Now()
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			n, err := io.Copy(io.Discard, response.Body)
			response.Body.Close()
			elapsed := time.Since(start)
			rate := float64(n) / elapsed.Seconds()
			t.Logf("configured %.2f MB/s; measured %.2f MB/s over %v", float64(kib*1024)/1e6, rate/1e6, elapsed)
			if err != nil {
				t.Fatalf("configured high rate could not finish: %v", err)
			}
			if n != int64(len(payload)) || rate < float64(kib*1024)*0.70 || rate > float64(kib*1024)*1.10 {
				t.Fatalf("limiter failed to track configured rate: %.2f MB/s", rate/1e6)
			}
		})
	}
}

func TestLiveSpeedStackedSharedAndSmallReads(t *testing.T) {
	for _, kind := range []string{"stacked", "shared-workers", "small-reads"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "speed.json")
			if err := os.WriteFile(path, []byte(`{"overall_kib":2929,"overrides":{"queue:1":2929}}`), 0600); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("pacing"), 1<<20)
			if kind == "shared-workers" {
				payload = payload[:len(payload)/2]
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
			defer server.Close()
			client := server.Client()
			client.Timeout = 6 * time.Second
			limitClient(client, &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "overall"}})
			if kind == "stacked" {
				limitClient(client, &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "queue:1"}})
			}
			workers := 1
			if kind == "shared-workers" {
				workers = 2
			}
			done := make(chan error, workers)
			start := time.Now()
			for i := 0; i < workers; i++ {
				go func() {
					response, err := client.Get(server.URL)
					if err != nil {
						done <- err
						return
					}
					defer response.Body.Close()
					var n int64
					if kind == "small-reads" {
						buffer := make([]byte, 1024)
						for {
							count, readErr := response.Body.Read(buffer)
							n += int64(count)
							if readErr != nil {
								if readErr != io.EOF {
									err = readErr
								}
								break
							}
						}
					} else {
						n, err = io.Copy(io.Discard, response.Body)
					}
					if err == nil && n != int64(len(payload)) {
						err = fmt.Errorf("received %d bytes, want %d", n, len(payload))
					}
					done <- err
				}()
			}
			for i := 0; i < workers; i++ {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			rate := float64(len(payload)*workers) / time.Since(start).Seconds()
			t.Logf("%s: %.2f MB/s", kind, rate/1e6)
			if rate < float64(2929*1024)*.70 || rate > float64(2929*1024)*1.10 {
				t.Fatalf("incorrect combined pacing: %.2f MB/s", rate/1e6)
			}
		})
	}
}

func TestLiveReadSizeTracksHighLowAndUnlimitedRates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.json")
	limit := &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "queue:1"}}
	for _, tc := range []struct {
		config string
		want   int64
	}{
		{`{"overrides":{"queue:1":2929}}`, 32768},
		{`{"overrides":{"queue:1":1}}`, 102},
		{`{}`, 32768},
	} {
		if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
			t.Fatal(err)
		}
		limit.liveChecked = time.Time{}
		got, err := limit.liveReadSize(context.Background())
		if err != nil || got != tc.want {
			t.Fatalf("read size %d, want %d: %v", got, tc.want, err)
		}
	}
}

func TestCanceledLiveWaitDoesNotLeaveSharedDebt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"overall_kib":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	limit := &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "overall"}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := limit.wait(ctx, 32768); err == nil {
		t.Fatal("wait did not cancel")
	}
	next, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if err := limit.wait(next, 1); err != nil {
		t.Fatalf("canceled read left debt in shared limiter: %v", err)
	}
}

func TestQueueDownloadSustainsLiveMegabyteRate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"overrides":{"queue:7":2929},"overall_kib":2929}`), 0600); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("verified"), 1<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"rate-v1"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, liveSpeedKey{}, path)
	ctx = context.WithValue(ctx, limiterContextKey{}, &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "overall"}})
	destination := filepath.Join(t.TempDir(), "download")
	start := time.Now()
	if err := runQueueDownloadWithProgress(ctx, queueJob{ID: 7, URL: server.URL, Destination: destination}, 1, io.Discard); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	data, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("speed-limited queue output differs", err)
	}
	rate := float64(len(payload)) / elapsed.Seconds()
	t.Logf("verified queue download with file and shared ceilings: %.2f MB/s", rate/1e6)
	if rate < float64(2929*1024)*.70 || rate > float64(2929*1024)*1.10 {
		t.Fatalf("queue failed to track 3 MB/s: %.2f MB/s", rate/1e6)
	}
}
