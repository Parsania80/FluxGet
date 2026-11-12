package media

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/networkproxy"
)

func nativeClient(t *testing.T) *Client {
	t.Helper()
	manifest := os.Getenv("FLUXGET_MEDIA_TOOLS")
	if manifest == "" {
		t.Skip("set FLUXGET_MEDIA_TOOLS to an explicit tools.json for real tool checks")
	}
	tools, err := LoadTools(manifest)
	if err != nil {
		t.Fatal(err)
	}
	proxy := os.Getenv("FLUXGET_MEDIA_PROXY")
	c, err := NewWithProxy(tools, networkproxy.Config{Enabled: proxy != "", URL: proxy})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

type countedResponse struct {
	http.ResponseWriter
	count *atomic.Int64
}

func (w countedResponse) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.count.Add(int64(n))
	return n, err
}

func TestNativeMediaTools(t *testing.T) {
	c := nativeClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	versions, err := c.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if versions["yt_dlp"] != "2026.10.26" || !strings.HasPrefix(versions["deno"], "deno 2.9.8 ") || !strings.Contains(versions["ffmpeg"], "N-128757-g4b42530309") {
		t.Fatal("tool versions do not match the spike lock")
	}
	t.Logf("native versions: %+v", versions)
	root := t.TempDir()
	vfile, afile := filepath.Join(root, "video.mp4"), filepath.Join(root, "audio.m4a")
	for _, args := range [][]string{
		{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10", "-t", "3", "-an", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", vfile},
		{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "3", "-vn", "-c:a", "aac", "-b:a", "128k", afile},
	} {
		if err := run(ctx, c.tools.FFmpeg, args, root, io.Discard, 16384); err != nil {
			t.Fatal(err)
		}
	}
	var served atomic.Int64
	files := http.FileServer(http.Dir(root))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { files.ServeHTTP(countedResponse{w, &served}, r) }))
	defer server.Close()
	info := map[string]any{"id": "AAAAAAAAAAA", "title": "Generated FluxGet fixture", "duration": 3, "extractor": "youtube", "extractor_key": "Youtube", "webpage_url": "https://www.youtube.com/watch?v=AAAAAAAAAAA", "formats": []map[string]any{
		{"format_id": "137", "url": server.URL + "/video.mp4", "ext": "mp4", "protocol": "http", "vcodec": "avc1.640028", "acodec": "none", "height": 90},
		{"format_id": "140", "url": server.URL + "/audio.m4a", "ext": "m4a", "protocol": "http", "vcodec": "none", "acodec": "mp4a.40.2", "abr": 128},
	}}
	data, _ := json.Marshal(info)
	infofile := filepath.Join(root, "fixture.json")
	if err := os.WriteFile(infofile, data, 0600); err != nil {
		t.Fatal(err)
	}
	v, err := parseVideo(data)
	if err != nil {
		t.Fatal(err)
	}
	// Only this generated fixture is used for diagnostics: never retain verbose
	// logs from a user's source URL. Confirm EJS and runtime presence explicitly.
	var diagnostics bytes.Buffer
	probeArgs := append(c.baseArgs(), "--verbose", "--simulate", "--load-info-json", infofile)
	if err := runStreams(ctx, c.tools.YTDLP, probeArgs, root, io.Discard, &diagnostics, 1<<20); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnostics.String(), "yt_dlp_ejs-0.8.0") || !strings.Contains(diagnostics.String(), "deno-2.9.8") {
		t.Fatal("pinned EJS or JavaScript runtime missing from native extractor diagnostics")
	}
	var proxyRequests atomic.Int64
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		request := r.Clone(r.Context())
		request.RequestURI = ""
		response, err := transport.RoundTrip(request)
		if err != nil {
			http.Error(w, "fixture proxy failed", 502)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	scratch := filepath.Join(root, "100% %(title)s")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []Selection{{"137", "140", "mp4", 0}, {"", "140", "mp3", 192}} {
		var limit int64
		if selection.Container == "mp4" {
			limit = 16 * 1024
			c.proxy = networkproxy.Config{Enabled: true, URL: proxy.URL}
		} else {
			c.proxy = networkproxy.Config{}
		}
		proxyBefore := proxyRequests.Load()
		before := served.Load()
		started := time.Now()
		events := make(chan Progress, 128)
		result, err := c.download(ctx, v, selection, DownloadOptions{Directory: scratch, LimitBytesPerSecond: limit, Events: events}, []string{"--load-info-json", infofile})
		if err != nil {
			t.Fatalf("%s native transfer: %v (scratch %s)", selection.Container, err, result.Workspace)
		}
		elapsed := time.Since(started)
		if selection.Container == "mp4" && proxyRequests.Load() <= proxyBefore {
			t.Fatal("real media transfer bypassed explicit proxy")
		}
		if selection.Container == "mp3" && proxyRequests.Load() != proxyBefore {
			t.Fatal("Direct media transfer used disabled proxy")
		}
		transferred := served.Load() - before
		if limit > 0 && float64(transferred)/elapsed.Seconds() > float64(limit)*1.25 {
			t.Fatalf("rate exceeded ceiling: %d bytes / %v", transferred, elapsed)
		}
		if limit > 0 && elapsed < time.Second {
			t.Fatal("small rate ceiling was not applied")
		}
		seenDownload, seenProcessing := false, false
		close(events)
		for event := range events {
			if event.Phase == "download" && event.Downloaded > 0 {
				seenDownload = true
			}
			if event.Phase == "processing" {
				seenProcessing = true
			}
		}
		if !seenDownload || !seenProcessing {
			t.Fatalf("missing structured phases: download=%v processing=%v", seenDownload, seenProcessing)
		}
		t.Logf("%s: %d network bytes in %v, cap=%d B/s, streams=%+v", selection.Container, transferred, elapsed, limit, result.Inspection.Streams)
	}
}

// Public YouTube acceptance is opt-in and never required by ordinary CI.
func TestYouTubePublicProbe(t *testing.T) {
	u := os.Getenv("FLUXGET_MEDIA_TEST_URL")
	if u == "" {
		t.Skip("requires explicit public YouTube sample selected by the operator")
	}
	c := nativeClient(t)
	v, err := c.Analyze(t.Context(), u)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("analyzed video %s: duration %.2fs, %d formats", v.ID, v.Duration, len(v.Formats))
	if len(v.Formats) < 2 {
		t.Fatal("expected multiple formats in selected sample")
	}
}
