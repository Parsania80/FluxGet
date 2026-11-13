package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaylistURLsAndBoundedMetadata(t *testing.T) {
	for _, raw := range []string{"https://www.youtube.com/playlist?list=PLexample", "https://youtu.be/AAAAAAAAAAA?list=PLexample", "https://m.youtube.com/watch?v=AAAAAAAAAAA&list=PLexample"} {
		got, err := NormalizePlaylistURL(raw)
		if err != nil || got != "https://www.youtube.com/playlist?list=PLexample" {
			t.Fatal(raw, got, err)
		}
	}
	for _, raw := range []string{"https://evil.invalid/playlist?list=PLexample", "http://youtube.com/playlist?list=PLexample", "https://user@youtube.com/playlist?list=PLexample", "https://youtube.com/playlist?list=PLone&list=PLtwo", "https://youtube.com/playlist?list=RDmix", "https://youtube.com/@channel?list=PLexample", "https://youtube.com/watch?list=PLexample", "https://youtube.com/playlist?list=PLx#fragment"} {
		if _, err := NormalizePlaylistURL(raw); err == nil {
			t.Fatal("accepted unsupported source", raw)
		}
	}
	entries := make([]any, 101)
	for i := range entries {
		entries[i] = map[string]any{"id": "AAAAAAAAAAA", "title": fmt.Sprint("Entry ", i+1), "playlist_index": i + 101, "url": "https://cdn.invalid/?secret=TOKEN"}
	}
	entries[1] = nil
	entries[2] = map[string]any{"id": "BBBBBBBBBBB", "title": "[Private video]", "playlist_index": 103}
	raw, _ := json.Marshal(map[string]any{"_type": "playlist", "id": "PLexample", "title": "Fixture playlist", "extractor": "youtube:tab", "extractor_key": "YoutubeTab", "webpage_url": "https://www.youtube.com/playlist?list=PLexample", "entries": entries})
	page, err := parsePlaylist(raw, "PLexample", 101)
	if err != nil || len(page.Entries) != 100 || !page.HasMore || page.Entries[0].Index != 101 || !page.Entries[1].Unavailable || !page.Entries[2].Unavailable {
		t.Fatal(page, err)
	}
	sanitized, _ := json.Marshal(page)
	if strings.Contains(string(sanitized), "TOKEN") {
		t.Fatal("signed URLs escaped flat metadata")
	}
	if _, err := parsePlaylist(raw, "PLdifferent", 101); err == nil {
		t.Fatal("wrong playlist accepted")
	}
	entries = append(entries, map[string]any{"id": "AAAAAAAAAAA"})
	raw, _ = json.Marshal(map[string]any{"_type": "playlist", "id": "PLexample", "entries": entries})
	if _, err := parsePlaylist(raw, "PLexample", 101); err == nil {
		t.Fatal("unbounded page accepted")
	}
}

func TestPlaylistClientUsesFlatBoundedExplicitIntake(t *testing.T) {
	executable, _ := os.Executable()
	c, err := New(Tools{YTDLP: executable, Deno: executable, FFmpeg: executable, FFprobe: executable})
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.Playlist(context.Background(), "https://www.youtube.com/playlist?list=PLexample", 101)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Index != 101 {
		t.Fatal(page, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Playlist(ctx, "https://www.youtube.com/playlist?list=PLexample", 1); err == nil {
		t.Fatal("canceled enumeration succeeded")
	}
}

// This fixture checks the pinned extractor's real pagination flags without
// contacting YouTube or resolving any selected video's formats.
func TestNativePlaylistPagination(t *testing.T) {
	c := nativeClient(t)
	entries := make([]map[string]any, 250)
	for i := range entries {
		entries[i] = map[string]any{"_type": "url", "id": "AAAAAAAAAAA", "title": fmt.Sprint("Entry ", i+1), "url": "https://www.youtube.com/watch?v=AAAAAAAAAAA", "ie_key": "Youtube"}
	}
	raw, _ := json.Marshal(map[string]any{"_type": "playlist", "id": "PLexample", "title": "Fixture playlist", "extractor": "youtube:tab", "extractor_key": "YoutubeTab", "webpage_url": "https://www.youtube.com/playlist?list=PLexample", "entries": entries})
	file := filepath.Join(t.TempDir(), "playlist.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := append(c.baseArgs(), "--yes-playlist", "--flat-playlist", "--lazy-playlist", "--playlist-items", "101:201", "--dump-single-json", "--skip-download", "--no-clean-info-json", "--load-info-json", file)
	data, err := capture(t.Context(), c.tools.YTDLP, args, "", 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	page, err := parsePlaylist(data, "PLexample", 101)
	if err != nil || len(page.Entries) != 100 || !page.HasMore || page.Entries[0].Index != 101 || page.Entries[99].Index != 200 {
		t.Fatalf("native pagination contract: %+v %v", page, err)
	}
}

func TestPlaylistMixRejectionKeepsSingleVideoAvailable(t *testing.T) {
	source := "https://www.youtube.com/watch?v=AAAAAAAAAAA&list=RDBBBBBBBBBBB&index=2"
	if _, err := NormalizePlaylistURL(source); !errors.Is(err, ErrMix) {
		t.Fatal("Mix rejection was not specific", err)
	}
	if got, err := NormalizeVideoURL(source); err != nil || got != "https://www.youtube.com/watch?v=AAAAAAAAAAA" {
		t.Fatal("single-video alternative was rejected", got, err)
	}
	executable, _ := os.Executable()
	client, err := New(Tools{YTDLP: executable, Deno: executable, FFmpeg: executable, FFprobe: executable})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Playlist(t.Context(), source, 1); !errors.Is(err, ErrMix) {
		t.Fatal("client discarded specific Mix error", err)
	}
	for _, source := range []string{"https://evil.invalid/playlist?list=RDBBBBBBBBBBB", "https://youtube.com/playlist?list=RDBBBBBBBBBBB&list=PLexample"} {
		if _, err := NormalizePlaylistURL(source); !errors.Is(err, ErrURL) {
			t.Fatal("invalid source was classified as a valid Mix link", err)
		}
	}
}
