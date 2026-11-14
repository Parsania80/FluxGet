//go:build linux

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type resizeServer struct {
	*httptest.Server
	payload  []byte
	release  chan struct{}
	mu       sync.Mutex
	starts   [][]int64
	active   int
	changed  atomic.Bool
	protocol atomic.Int32
}

func newResizeServer(t *testing.T, http2 ...bool) *resizeServer {
	t.Helper()
	s := &resizeServer{payload: bytes.Repeat([]byte("resize!"), 1200000), release: make(chan struct{}), starts: make([][]int64, 4)}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.protocol.Store(int32(r.ProtoMajor))
		w.Header().Set("ETag", `"resize-v1"`)
		if s.changed.Load() {
			w.Header().Set("ETag", `"resize-v2"`)
		}
		if r.Header.Get("Range") == "bytes=0-0" {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(s.payload))
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < 0 || end >= int64(len(s.payload)) || end < start {
			http.Error(w, "invalid test range", http.StatusBadRequest)
			return
		}
		index := int(start / (int64(len(s.payload)) / 4))
		s.mu.Lock()
		s.starts[index] = append(s.starts[index], start)
		s.active++
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.active--; s.mu.Unlock() }()
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(s.payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		n := min(int64(64<<10), end-start+1)
		_, _ = w.Write(s.payload[start : start+n])
		w.(http.Flusher).Flush()
		select {
		case <-s.release:
			_, _ = w.Write(s.payload[start+n : end+1])
		case <-r.Context().Done():
		}
	}))
	if len(http2) > 0 && http2[0] {
		s.EnableHTTP2 = true
		s.StartTLS()
	} else {
		s.Start()
	}
	t.Cleanup(s.Close)
	return s
}

func (s *resizeServer) activeCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.active }

func startResizeTransfer(t *testing.T, s *resizeServer) (context.CancelFunc, *workerControl, *queueProgressWriter, *transferMeasurements, *requestBudget, string, <-chan error) {
	t.Helper()
	control := newWorkerControl(4, 1)
	m := &transferMeasurements{}
	budget := newRequestBudget(requestPolicy{Mode: "automatic", Total: 4, PerOrigin: 4})
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, workerControlKey{}, control)
	ctx = context.WithValue(ctx, measurementContextKey{}, m)
	ctx = context.WithValue(ctx, budgetContextKey{}, budget)
	client := defaultDownloadClient()
	if s.TLS != nil {
		client = s.Client()
	}
	client.Transport = &requestTransport{base: client.Transport, budget: budget, job: 1, metrics: m}
	writer := &queueProgressWriter{updates: make(chan queueProgress, 1)}
	dest := filepath.Join(t.TempDir(), "resized.bin")
	done := make(chan error, 1)
	go func() {
		_, err := downloadFresh(ctx, client, s.URL, dest, "", writer, 4)
		client.CloseIdleConnections()
		done <- err
	}()
	t.Cleanup(cancel)
	return cancel, control, writer, m, budget, dest, done
}

func waitResizeWorkers(t *testing.T, s *resizeServer, writer *queueProgressWriter, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p := writer.segments.Load()
		if p != nil && p.Target == n && len(p.Parts) == 4 && s.activeCount() == n {
			active, retiring := 0, false
			for _, part := range p.Parts {
				if part.State == "downloading" && part.Downloaded >= 64<<10 {
					active++
				}
				if part.State == "retiring" {
					retiring = true
				}
			}
			if active == n && !retiring {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker target %d not reached: server active=%d snapshot=%+v", n, s.activeCount(), writer.segments.Load())

}

func TestLiveWorkerResizePreservesPrefixesAndOutput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newResizeServer(t)
	_, control, writer, m, budget, dest, done := startResizeTransfer(t, s)
	waitResizeWorkers(t, s, writer, 1)
	path, _ := checkpointLocation(dest)
	for _, target := range []int{2, 4, 1, 4, 1, 2, 4, 1} {
		if err := control.set(4, target); err != nil {
			t.Fatal(err)
		}
		waitResizeWorkers(t, s, writer, target)
		if target == 1 {
			c, err := loadCheckpoint(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(dest + ".part")
			if err != nil {
				t.Fatal(err)
			}
			for _, chunk := range c.Chunks[1:] {
				if chunk.savedLength() < 64<<10 || chunk.SHA256 != "" {
					t.Fatalf("retired prefix was lost or marked complete: %+v", chunk)
				}
				digest, err := chunkPrefixDigest(f, chunk)
				if err != nil || digest != chunk.savedDigest() {
					t.Fatal("retired prefix not durably verified", err)
				}
			}
			f.Close()
		}
	}
	s.mu.Lock()
	step := int64(len(s.payload)) / 4
	for i, starts := range s.starts {
		for attempt, start := range starts {
			want := int64(i) * step
			if i > 0 {
				want += int64(attempt) * (64 << 10)
			}
			if start != want {
				t.Errorf("part %d attempt %d starts at %d, want saved suffix %d", i, attempt, start, want)
			}
		}
	}
	s.mu.Unlock()
	close(s.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("resized transfer did not finish")
	}
	requireContents(t, dest, s.payload)
	requireMissing(t, path)
	if b, _ := budget.snapshot(); b.Active != 0 || b.Waiting != 0 {
		t.Fatal("retirement leaked request leases")
	}
	if got := m.snapshot(); got.Failures != 0 || got.RetryWait != 0 || got.Accepted != int64(len(s.payload)) || got.WorkerChanges == 0 {
		t.Fatalf("retirement treated as failure/retry or duplicated bytes: %+v", got)
	}
}

func TestWorkerRetirementCheckpointFailureStopsTransfer(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions are ineffective as root")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newResizeServer(t)
	_, control, writer, _, budget, dest, done := startResizeTransfer(t, s)
	if err := control.set(4, 4); err != nil {
		t.Fatal(err)
	}
	waitResizeWorkers(t, s, writer, 4)
	path, _ := checkpointLocation(dest)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(filepath.Dir(path), 0700)
	if err := control.set(4, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "save chunk progress") {
			t.Fatalf("retirement hid checkpoint error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("checkpoint failure left workers running")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed retirement damaged previous checkpoint", err)
	}
	requireMissing(t, dest)
	if b, _ := budget.snapshot(); b.Active != 0 || b.Waiting != 0 {
		t.Fatal("failed retirement leaked leases")
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	close(s.release)
	if _, err := resumeDownloadWithLimit(context.Background(), defaultDownloadClient(), dest, "", io.Discard, 1); err != nil {
		t.Fatal("failed retirement could not recover", err)
	}
	requireContents(t, dest, s.payload)
}

func TestWorkerResizeHTTP2(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newResizeServer(t, true)
	_, control, writer, m, budget, dest, done := startResizeTransfer(t, s)
	defer func() {
		select {
		case err := <-done:
			t.Logf("transfer ended: %v", err)
		default:
		}
	}()
	waitResizeWorkers(t, s, writer, 1)
	for _, target := range []int{2, 4, 1, 4} {
		if err := control.set(4, target); err != nil {
			t.Fatal(err)
		}
		waitResizeWorkers(t, s, writer, target)
	}
	close(s.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP/2 workers failed to drain")
	}
	if s.protocol.Load() != 2 {
		t.Fatal("test did not negotiate HTTP/2")
	}
	if b, _ := budget.snapshot(); b.Active != 0 || b.Waiting != 0 {
		t.Fatal("HTTP/2 retirement leaked leases")
	}
	if got := m.snapshot(); got.Failures != 0 || got.RetryWait != 0 {
		t.Fatal("HTTP/2 retirement counted as network failure", got)
	}
	requireContents(t, dest, s.payload)
}

func TestLiveResizeRejectsChangedRemoteAndSavedPrefix(t *testing.T) {
	for _, change := range []string{"remote", "local"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			s := newResizeServer(t)
			_, control, writer, _, _, dest, done := startResizeTransfer(t, s)
			control.set(4, 4)
			waitResizeWorkers(t, s, writer, 4)
			control.set(4, 1)
			waitResizeWorkers(t, s, writer, 1)
			if change == "remote" {
				s.changed.Store(true)
			} else {
				f, err := os.OpenFile(dest+".part", os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteAt([]byte("!"), int64(len(s.payload))/4)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			control.set(4, 4)
			select {
			case err := <-done:
				if err == nil || onlyCancellation(err) {
					t.Fatalf("changed representation accepted: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("changed transfer did not stop")
			}
			requireMissing(t, dest)
		})
	}
}

func TestWorkerResizeValidationAndSampling(t *testing.T) {
	control := newWorkerControl(4, 1)
	for _, v := range [][2]int{{0, 1}, {17, 1}, {4, 0}, {4, 5}} {
		if control.set(v[0], v[1]) == nil {
			t.Fatal("accepted invalid worker target", v)
		}
	}
	if got := control.settings.Load(); got.Maximum != 4 || got.Target != 1 {
		t.Fatal("invalid target changed control")
	}
	var sampler observationSampler
	now := time.Unix(100, 0)
	sampler.sample(now, measurementSnapshot{}, 1)
	o, ok := sampler.sample(now.Add(10*time.Second), measurementSnapshot{Accepted: 1000, WorkerChanges: 2}, 1)
	if !ok || o.Valid || o.Reason != "worker count changed" {
		t.Fatal("resizing was treated as a stable measurement", o)
	}
	if onlyCancellation(fmt.Errorf("retired: %w", errors.Join(context.Canceled, io.ErrClosedPipe))) {
		t.Fatal("retirement suppressed a joined storage error")
	}
}

type retirementCrashWriter struct {
	queueProgressWriter
	phase, signal string
	triggered     atomic.Bool
}

func (w *retirementCrashWriter) ReportSegments(snapshot segmentSnapshot) {
	w.queueProgressWriter.ReportSegments(snapshot)
	ready := false
	if w.phase == "retiring" {
		for _, part := range snapshot.Parts {
			if part.State == "retiring" {
				ready = true
			}
		}
	} else if len(snapshot.Parts) == 4 {
		ready = true
		for _, part := range snapshot.Parts[1:] {
			if part.State != "waiting" || part.Downloaded < 64<<10 {
				ready = false
			}
		}
	}
	if ready && w.triggered.CompareAndSwap(false, true) {
		if err := os.WriteFile(w.signal, []byte("ready"), 0600); err != nil {
			panic(err)
		}
		select {} // The parent kills this isolated process at the retirement boundary.
	}
}

func TestWorkerResizeCrashRecovery(t *testing.T) {
	if os.Getenv("FLUXGET_RESIZE_CHILD") == "1" {
		control := newWorkerControl(4, 4)
		ctx := context.WithValue(context.Background(), workerControlKey{}, control)
		writer := &retirementCrashWriter{queueProgressWriter: queueProgressWriter{updates: make(chan queueProgress, 1)}, phase: os.Getenv("FLUXGET_RESIZE_PHASE"), signal: os.Getenv("FLUXGET_RESIZE_SIGNAL")}
		go func() {
			for {
				p := writer.segments.Load()
				ready := p != nil && len(p.Parts) == 4
				if ready {
					for _, part := range p.Parts {
						if part.Downloaded < 64<<10 {
							ready = false
						}
					}
				}
				if ready {
					control.set(4, 1)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		_, err := downloadFresh(ctx, defaultDownloadClient(), os.Getenv("FLUXGET_RESIZE_URL"), os.Getenv("FLUXGET_RESIZE_DEST"), "", writer, 4)
		t.Fatalf("child completed before kill: %v", err)
	}
	for _, phase := range []string{"retiring", "saved"} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			s := newResizeServer(t)
			dest := filepath.Join(t.TempDir(), "crashed.bin")
			signal := dest + ".signal"
			cmd := exec.Command(os.Args[0], "-test.run=^TestWorkerResizeCrashRecovery$")
			cmd.Env = append(os.Environ(), "FLUXGET_RESIZE_CHILD=1", "FLUXGET_RESIZE_PHASE="+phase, "FLUXGET_RESIZE_SIGNAL="+signal, "FLUXGET_RESIZE_URL="+s.URL, "FLUXGET_RESIZE_DEST="+dest)
			var output bytes.Buffer
			cmd.Stderr = &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					cmd.Process.Kill()
					cmd.Wait()
				}
			}()
			waitQueue(t, func() bool { _, err := os.Stat(signal); return err == nil })
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("child was not killed")
			}
			path, _ := checkpointLocation(dest)
			c := assertSavedChunks(t, path, dest, s.payload)
			if len(c.Chunks) != 4 {
				t.Fatalf("part layout changed after retirement crash: %+v; %s", c, output.String())
			}
			close(s.release)
			ctx := context.WithValue(context.Background(), workerControlKey{}, newWorkerControl(4, 1))
			if _, err := resumeDownloadWithLimit(ctx, defaultDownloadClient(), dest, "", io.Discard, 1); err != nil {
				t.Fatal(err)
			}
			requireContents(t, dest, s.payload)
			requireMissing(t, path)
		})
	}
}
