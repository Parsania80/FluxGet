// fluxget-media-spike is a development probe, not a second persistent queue.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
)

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	proxy := flag.String("proxy", "", "HTTP/HTTPS/SOCKS5 proxy address; empty selects Direct")
	manifest := flag.String("tools", "", "Absolute path to the provisioned tools.json")
	mode := flag.String("mode", "probe", "probe, analyze, video, or mp3")
	u := flag.String("url", "", "Public YouTube video URL (explicit opt-in network probe)")
	dir := flag.String("directory", "", "Existing absolute scratch parent for download modes")
	limit := flag.Int64("limit", 0, "Per-process bytes/second; zero unlimited")
	flag.Parse()
	tools, err := media.LoadTools(*manifest)
	if err != nil {
		return err
	}
	client, err := media.NewWithProxy(tools, networkproxy.Config{Enabled: *proxy != "", URL: *proxy})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if *mode == "probe" {
		versions, err := client.Probe(ctx)
		if err != nil {
			return err
		}
		return encoder.Encode(versions)
	}
	if *mode != "analyze" && *mode != "video" && *mode != "mp3" {
		return fmt.Errorf("unsupported probe mode")
	}
	video, err := client.Analyze(ctx, *u)
	if err != nil {
		return err
	}
	if *mode == "analyze" {
		return encoder.Encode(video)
	}
	// The spike chooses explicit IDs; the later UI will expose all available
	// combinations rather than adding an opaque downloader format expression.
	selection := media.Selection{Container: "mp4"}
	bestHeight, bestAudio := 0, float64(-1)
	for _, format := range video.Formats {
		if format.VideoCodec == "none" && (format.AudioCodec == "mp4a.40.2" || format.AudioCodec == "aac") && format.AudioBitrate > bestAudio {
			bestAudio, selection.AudioFormat = format.AudioBitrate, format.ID
		}
		if format.AudioCodec == "none" && len(format.VideoCodec) >= 4 && format.VideoCodec[:4] == "avc1" && format.Height <= 1080 && format.Height > bestHeight {
			bestHeight, selection.VideoFormat = format.Height, format.ID
		}
	}
	if *mode == "mp3" {
		selection.VideoFormat = ""
		selection.Container = "mp3"
		selection.MP3Kbps = 192
	}
	if *mode == "video" && selection.VideoFormat == "" {
		return media.ErrSelection
	}
	result, err := client.Download(ctx, video, selection, media.DownloadOptions{Directory: *dir, LimitBytesPerSecond: *limit})
	if err != nil {
		if result.Workspace != "" {
			fmt.Fprintln(os.Stderr, "Scratch files retained:", result.Workspace)
		}
		return err
	}
	return encoder.Encode(result)
}
