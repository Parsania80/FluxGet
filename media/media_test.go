package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixtureMetadata = `{"id":"AAAAAAAAAAA","title":"Fixture","duration":3,"availability":"public","formats":[{"format_id":"137","ext":"mp4","vcodec":"avc1.640028","acodec":"none","height":1080,"protocol":"https","url":"https://cdn.invalid/private?secret=TOKEN"},{"format_id":"140","ext":"m4a","vcodec":"none","acodec":"mp4a.40.2","protocol":"https"},{"format_id":"251","ext":"webm","vcodec":"none","acodec":"opus","protocol":"https"},{"format_id":"drm","has_drm":true,"protocol":"https"}]}`

// This test executable doubles as a fake tool, including actual child processes.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "media-helper":
			helper(os.Args[2:])
			return
		case "--ignore-config":
			fakeYTDLP(os.Args[2:])
			return
		case "--version", "-version":
			fmt.Println("fixture 1.0")
			return
		case "-v":
			container := "mov,mp4,m4a,3gp,3g2,mj2"
			streams := `[{"codec_type":"video","codec_name":"h264","height":1080},{"codec_type":"audio","codec_name":"aac"}]`
			if strings.HasSuffix(os.Args[len(os.Args)-1], ".mp3") {
				container = "mp3"
				streams = `[{"codec_type":"audio","codec_name":"mp3"}]`
			}
			fmt.Printf(`{"format":{"duration":"3.0","format_name":%q},"streams":%s}`, container, streams)
			return
		}
	}
	os.Exit(m.Run())
}

func helper(args []string) {
	switch args[0] {
	case "flood":
		fmt.Print(strings.Repeat("x", 2<<20))
	case "error":
		fmt.Fprintln(os.Stderr, "https://cdn.invalid/file?secret=TOKEN")
		if len(args) > 1 {
			fmt.Fprintln(os.Stderr, args[1])
		}
		os.Exit(17)
	case "tree", "exit-tree":
		child := exec.Command(os.Args[0], "media-helper", "ticker", args[1])
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		// Wait for a real child write, not merely successful process creation.
		for n := 0; n < 300; n++ {
			if info, err := os.Stat(args[1]); err == nil && info.Size() > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if args[0] == "exit-tree" {
			return
		}
		for {
			time.Sleep(time.Second)
		}
	case "ticker":
		file, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			os.Exit(3)
		}
		for {
			_, _ = file.Write([]byte("x"))
			time.Sleep(15 * time.Millisecond)
		}
	}
}

func fakeYTDLP(args []string) {
	for _, a := range args {
		if a == "--flat-playlist" {
			joined := strings.Join(args, " ")
			for _, want := range []string{"--yes-playlist", "--lazy-playlist", "--playlist-items 101:201", "--skip-download", "--proxy ", "--use-extractors youtube.*"} {
				if !strings.Contains(joined, want) {
					os.Exit(19)
				}
			}
			fmt.Print(`{"_type":"playlist","id":"PLexample","title":"Fixture","entries":[{"id":"AAAAAAAAAAA","title":"Video","playlist_index":101}]}`)
			return
		}
	}

	for _, a := range args {
		if a == "--dump-single-json" {
			fmt.Println(fixtureMetadata)
			return
		}
	}
	var output, container string
	for i, a := range args {
		if a == "--output" {
			output = args[i+1]
		}
		if a == "--remux-video" || a == "--audio-format" {
			container = args[i+1]
		}
	}
	if container == "" {
		os.Exit(5)
	}
	output = strings.ReplaceAll(output, "%(ext)s", container)
	_ = os.WriteFile(output, []byte("fixture bytes"), 0600)
	fmt.Println(`FG_PHASE:download`)
	fmt.Println(`FG_PROGRESS:{"downloaded_bytes":12,"total_bytes":12,"speed":64,"eta":0,"filename":"secret"}`)
	fmt.Println(`FG_PHASE:processing`)
	encoded, _ := json.Marshal(output)
	fmt.Printf("FG_RESULT:%s\n", encoded)
}

func fakeClient(t *testing.T) *Client {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Tools{executable, executable, executable, executable})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestURLAndMetadataBoundaries(t *testing.T) {
	for _, u := range []string{"https://youtu.be/AAAAAAAAAAA", "https://www.youtube.com/watch?v=AAAAAAAAAAA&list=PL123&tracking=secret", "https://youtube.com/shorts/AAAAAAAAAAA"} {
		got, err := NormalizeVideoURL(u)
		if err != nil || got != "https://www.youtube.com/watch?v=AAAAAAAAAAA" {
			t.Fatalf("normalize: %q %v", got, err)
		}
	}
	for _, u := range []string{"https://youtube.com.evil/watch?v=AAAAAAAAAAA", "http://youtube.com/watch?v=AAAAAAAAAAA", "https://user:secret@youtube.com/watch?v=AAAAAAAAAAA", "https://youtube.com:443/watch?v=AAAAAAAAAAA", "https://youtube.com/watch?v=AAAAAAAAAAA&v=BBBBBBBBBBB", "https://youtube.com/watch?v=--exec%20evil", "https://youtu.be/AAAAAAAAAAA/extra", "file:///tmp/video", "https://youtube.com/watch?v=AAAAAAAAAAA#fragment"} {
		if _, err := NormalizeVideoURL(u); !errors.Is(err, ErrURL) {
			t.Fatalf("accepted unsupported URL: %v", err)
		}
	}
	if _, err := NormalizeVideoURL("https://youtube.com/playlist?list=PL123"); !errors.Is(err, ErrPlaylist) {
		t.Fatal(err)
	}
	v, err := parseVideo([]byte(fixtureMetadata))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(v)
	if bytes.Contains(encoded, []byte("TOKEN")) || bytes.Contains(encoded, []byte("cdn.invalid")) || len(v.Formats) != 3 {
		t.Fatal("metadata retained a signed URL or DRM format")
	}
	for _, data := range []string{`{}`, strings.Replace(fixtureMetadata, `"public"`, `"private"`, 1), strings.Replace(fixtureMetadata, `"duration":3`, `"duration":null`, 1), strings.Replace(fixtureMetadata, `"availability":"public"`, `"is_live":true`, 1)} {
		if _, err := parseVideo([]byte(data)); err == nil {
			t.Fatal("accepted unavailable/live/invalid metadata")
		}
	}
}

func TestSelectionChecksActualFormats(t *testing.T) {
	v, _ := parseVideo([]byte(fixtureMetadata))
	for _, s := range []Selection{{"137", "140", "mp4", 0}, {"137", "251", "mkv", 0}, {"", "140", "m4a", 0}, {"", "251", "opus", 0}, {"", "140", "mp3", 192}} {
		if _, err := s.validate(v); err != nil {
			t.Fatalf("valid selection %+v: %v", s, err)
		}
	}
	for _, s := range []Selection{{"137", "251", "mp4", 0}, {"137", "140", "webm", 0}, {"missing", "140", "mp4", 0}, {"137+best", "140", "mp4", 0}, {"", "140", "opus", 0}, {"", "140", "mp3", 999}, {"", "drm", "m4a", 0}} {
		if _, err := s.validate(v); !errors.Is(err, ErrSelection) {
			t.Fatalf("bad selection accepted: %+v", s)
		}
	}
}

func TestStructuredProgressFragmentedAndBounded(t *testing.T) {
	events := make(chan Progress, 5)
	p := newProgressParser(events)
	for _, piece := range []string{"FG_PRO", "GRESS:{\"downloaded_bytes\":2,\"total_bytes\":null,\"total_bytes_estimate\":4,\"speed\":null,\"eta\":null}\r\nFG_PHASE:processing\nFG_RESULT:\"/tmp/out.mp4\"\n"} {
		if _, err := p.Write([]byte(piece)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	if got := <-events; got.Downloaded != 2 || got.Total != 4 || got.Speed != 0 {
		t.Fatal(got)
	}
	if got := <-events; got.Phase != "processing" {
		t.Fatal(got)
	}
	// A blocked display must not block subprocess draining.
	p = newProgressParser(make(chan Progress))
	if _, err := p.Write([]byte("FG_PHASE:download\n")); err != nil {
		t.Fatal(err)
	}
	p = newProgressParser(nil)
	if _, err := p.Write([]byte(strings.Repeat("x", (1<<20)+1))); !errors.Is(err, ErrOutputLimit) {
		t.Fatal(err)
	}
	p = newProgressParser(nil)
	if _, err := p.Write([]byte("FG_PROGRESS:{broken}\n")); err == nil {
		t.Fatal("malformed progress accepted")
	}
	p = newProgressParser(nil)
	_, _ = p.Write([]byte("FG_RESULT:\"incomplete"))
	if p.finish() == nil {
		t.Fatal("truncated record accepted")
	}
}

func TestFakeAnalyzeDownloadAndProbe(t *testing.T) {
	c := fakeClient(t)
	versions, err := c.Probe(t.Context())
	if err != nil || len(versions) != 4 {
		t.Fatalf("probe: %v", err)
	}
	v, err := c.Analyze(t.Context(), "https://youtu.be/AAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Analyze(t.Context(), "https://youtu.be/BBBBBBBBBBB"); !errors.Is(err, ErrMetadata) {
		t.Fatalf("source mismatch: %v", err)
	}
	for _, s := range []Selection{{"137", "140", "mp4", 0}, {"", "140", "mp3", 192}} {
		r, err := c.Download(t.Context(), v, s, DownloadOptions{Directory: t.TempDir(), LimitBytesPerSecond: 1024, Events: make(chan Progress)})
		if err != nil || r.Output == "" {
			t.Fatalf("download: %+v %v", r, err)
		}
	}
}

func TestToolErrorsAndOutputLimitAreRedacted(t *testing.T) {
	exe, _ := os.Executable()
	err := run(t.Context(), exe, []string{"media-helper", "error"}, "", io.Discard, 1024)
	var tool *ToolError
	if !errors.As(err, &tool) || tool.ExitCode != 17 || strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("error: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = run(ctx, exe, []string{"media-helper", "flood"}, "", io.Discard, 1024)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("output limit: %v", err)
	}
}

func TestToolFailureDiagnosticsAreActionableAndPrivate(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"Read timed out. (read timeout=15.0)", "Media connection timed out"},
		{"HTTP Error 403: Forbidden", "media server denied access"},
		{"HTTP Error 429: Too Many Requests", "limiting requests"},
		{"Connection refused", "check that your proxy is running"},
		{"Requested format is not available", "choose another format"},
		{"This video is unavailable", "video is unavailable"},
		{"No space left on device", "Not enough disk space"},
		{"ffprobe and ffmpeg not found", "tools are missing"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			d := &toolDiagnostic{}
			// Exercise split writes and a large prefix without retaining raw logs.
			d.Write(bytes.Repeat([]byte("x"), 16000))
			raw := "https://cdn.invalid/private?secret=TOKEN\n" + tc.raw + "\npassword=SECRET"
			for _, b := range []byte(raw) {
				d.Write([]byte{b})
			}
			message := (&ToolError{ExitCode: 1, reason: d.reason()}).Error()
			if len(d.tail) > 8192 || !strings.Contains(message, tc.want) || strings.Contains(message, "TOKEN") || strings.Contains(message, "SECRET") || strings.Contains(message, "cdn.invalid") {
				t.Fatalf("unsafe or unhelpful diagnostic: %q", message)
			}
		})
	}
	exe, _ := os.Executable()
	err := run(t.Context(), exe, []string{"media-helper", "error", "Read timed out; proxy=https://user:SECRET@proxy.invalid/?token=TOKEN"}, "", io.Discard, 1024)
	var tool *ToolError
	if !errors.As(err, &tool) || tool.ExitCode != 17 || !strings.Contains(err.Error(), "Media connection timed out") || strings.Contains(err.Error(), "TOKEN") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("subprocess diagnostic: %v", err)
	}
}

func TestProcessTreeCancellationAndNormalExit(t *testing.T) {
	for _, mode := range []string{"tree", "exit-tree"} {
		t.Run(mode, func(t *testing.T) {
			exe, _ := os.Executable()
			marker := filepath.Join(t.TempDir(), "child-writes")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- run(ctx, exe, []string{"media-helper", mode, marker}, "", io.Discard, 1024) }()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if info, err := os.Stat(marker); err == nil && info.Size() > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child never started")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "tree" {
				cancel()
			}
			err := <-done
			if mode == "tree" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode == "exit-tree" && err != nil {
				t.Fatal(err)
			}
			before, _ := os.Stat(marker)
			time.Sleep(150 * time.Millisecond)
			after, _ := os.Stat(marker)
			if before.Size() != after.Size() {
				t.Fatal("orphan child survived process-tree cleanup")
			}
		})
	}
}

func TestInspectionRejectsFalseSuccess(t *testing.T) {
	var i Inspection
	_ = json.Unmarshal([]byte(`{"streams":[{"codec_type":"audio"}],"format":{"format_name":"mp3","duration":"3"}}`), &i)
	if err := i.validate(3, Selection{Container: "mp3"}); err != nil {
		t.Fatal(err)
	}
	if i.validate(30, Selection{Container: "mp3"}) == nil || i.validate(3, Selection{Container: "mp4", VideoFormat: "137"}) == nil {
		t.Fatal("invalid output passed inspection")
	}
}

func TestFractionalFragmentEstimateIsSupported(t *testing.T) {
	events := make(chan Progress, 1)
	parser := newProgressParser(events)
	if _, err := parser.Write([]byte("FG_PROGRESS:{\"format_id\":\"137\",\"downloaded_bytes\":100,\"total_bytes_estimate\":1000.75,\"speed\":32,\"eta\":1}\n")); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event.Total != 1000 || event.FormatID != "137" {
		t.Fatal("fragment estimate or format counter lost", event)
	}
}
