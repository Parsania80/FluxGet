package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Parsania80/FluxGet/networkproxy"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var ErrTools = errors.New("media tools are missing or invalid; use the stage-1 setup helper")

type Tools struct {
	YTDLP   string `json:"yt_dlp"`
	Deno    string `json:"deno"`
	FFmpeg  string `json:"ffmpeg"`
	FFprobe string `json:"ffprobe"`
}

type Client struct {
	tools Tools
	proxy networkproxy.Config
	slot  chan struct{}
}

// New requires explicit paths. There is deliberately no PATH search or updater.
func New(tools Tools) (*Client, error) { return NewWithProxy(tools, networkproxy.Config{}) }

func NewWithProxy(tools Tools, proxy networkproxy.Config) (*Client, error) {
	proxy, err := proxy.Normalize()
	if err != nil {
		return nil, err
	}
	for _, path := range []string{tools.YTDLP, tools.Deno, tools.FFmpeg, tools.FFprobe} {
		info, err := os.Stat(path)
		if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() {
			return nil, ErrTools
		}
	}
	if filepath.Dir(tools.FFmpeg) != filepath.Dir(tools.FFprobe) {
		return nil, ErrTools
	}
	return &Client{tools: tools, proxy: proxy, slot: make(chan struct{}, 1)}, nil
}

func LoadTools(manifest string) (Tools, error) {
	var value struct {
		Tools Tools `json:"tools"`
	}
	info, err := os.Stat(manifest)
	if err != nil || info.Size() > 16384 {
		return Tools{}, ErrTools
	}
	data, err := os.ReadFile(manifest)
	if err != nil || json.Unmarshal(data, &value) != nil {
		return Tools{}, ErrTools
	}
	return value.Tools, nil
}

func (c *Client) acquire(ctx context.Context) (func(), error) {
	select {
	case c.slot <- struct{}{}:
		return func() { <-c.slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Probe checks executability and records exact native versions. A successful
// probe does not establish that a particular YouTube video can be extracted.
func (c *Client) Probe(ctx context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	versions := make(map[string]string)
	for _, tool := range []struct{ name, path, flag string }{{"yt_dlp", c.tools.YTDLP, "--version"}, {"deno", c.tools.Deno, "--version"}, {"ffmpeg", c.tools.FFmpeg, "-version"}, {"ffprobe", c.tools.FFprobe, "-version"}} {
		data, err := capture(ctx, tool.path, []string{tool.flag}, "", 16384)
		if err != nil {
			return nil, fmt.Errorf("%s probe: %w", tool.name, err)
		}
		line := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)[0]
		if line == "" || len(line) > 512 {
			return nil, ErrTools
		}
		versions[tool.name] = line
	}
	return versions, nil
}

func (c *Client) baseArgs() []string {
	proxy, _ := c.proxy.Argument()
	return []string{"--ignore-config", "--proxy", proxy, "--no-plugin-dirs", "--no-remote-components", "--no-cache-dir", "--no-js-runtimes", "--js-runtimes", "deno:" + c.tools.Deno, "--ffmpeg-location", filepath.Dir(c.tools.FFmpeg), "--no-playlist", "--use-extractors", "youtube", "--socket-timeout", "15", "--retries", "1", "--fragment-retries", "1", "--no-warnings"}
}

func (c *Client) Analyze(ctx context.Context, rawURL string) (Video, error) {
	u, err := NormalizeVideoURL(rawURL)
	if err != nil {
		return Video{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	release, err := c.acquire(ctx)
	if err != nil {
		return Video{}, err
	}
	defer release()
	args := append(c.baseArgs(), "--dump-single-json", "--skip-download", "--", u)
	data, err := capture(ctx, c.tools.YTDLP, args, "", 16<<20)
	if err != nil {
		return Video{}, err
	}
	v, err := parseVideo(data)
	if err != nil || "https://www.youtube.com/watch?v="+v.ID != u {
		return Video{}, ErrMetadata
	}
	return v, nil
}

type Progress struct {
	FormatID   string  `json:"format_id,omitempty"`
	Phase      string  `json:"phase"`
	Downloaded int64   `json:"downloaded_bytes"`
	Total      int64   `json:"total_bytes,omitempty"`
	Speed      float64 `json:"bytes_per_second,omitempty"`
	ETA        float64 `json:"eta_seconds,omitempty"`
}

// DownloadOptions is immutable for a run. Stage 3 must restart/pause a run to
// change its cap; this spike does not claim live or shared-engine speed limits.
type DownloadOptions struct {
	Workspace           string          // Optional existing job-owned workspace, validated by its queue owner.
	Directory           string          // Existing absolute scratch parent; creates a private child.
	LimitBytesPerSecond int64           // Zero means unlimited. One fragment/transfer only.
	Events              chan<- Progress // Nonblocking, lossy display updates; never state authority.
}

type Result struct {
	Workspace  string     `json:"workspace"`
	Output     string     `json:"output"`
	Inspection Inspection `json:"inspection"`
}

// Download produces a validated scratch artifact, not a final queue download.
// It never publishes to a user-selected name or automatically removes partials.
func (c *Client) Download(ctx context.Context, video Video, selection Selection, options DownloadOptions) (Result, error) {
	return c.download(ctx, video, selection, options, []string{"--", "https://www.youtube.com/watch?v=" + video.ID})
}

// sourceArgs is private so native tests can exercise the exact transfer path
// with generated local info JSON; public callers can only supply YouTube URLs.
func (c *Client) download(ctx context.Context, video Video, selection Selection, options DownloadOptions, sourceArgs []string) (Result, error) {
	if !videoID.MatchString(video.ID) {
		return Result{}, ErrMetadata
	}
	selector, err := selection.validate(video)
	if err != nil {
		return Result{}, err
	}
	if !filepath.IsAbs(options.Directory) || options.LimitBytesPerSecond < 0 || options.LimitBytesPerSecond > 1<<40 {
		return Result{}, errors.New("invalid media scratch directory or speed ceiling")
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	workspace := options.Workspace
	if workspace == "" {
		workspace, err = os.MkdirTemp(options.Directory, "fluxget-media-")
	} else {
		info, e := os.Lstat(workspace)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !filepath.IsAbs(workspace) || filepath.Dir(workspace) != filepath.Clean(options.Directory) {
			err = errors.New("invalid owned media workspace")
		}
	}
	if err != nil {
		return Result{}, errors.New("cannot open private media workspace")
	}
	result := Result{Workspace: workspace}
	// Only the extension is a template; percent signs in user paths are literal.
	outputTemplate := filepath.Join(strings.ReplaceAll(workspace, "%", "%%"), "asset.%(ext)s")
	args := append(c.baseArgs(), "--quiet", "--progress", "--newline", "--progress-delta", "0.2", "--concurrent-fragments", "1", "--downloader", "native", "--no-overwrites", "--continue", "--format", selector, "--output", outputTemplate, "--print", "before_dl:FG_PHASE:download", "--print", "after_move:FG_RESULT:%(filepath)j", "--progress-template", `download:FG_PROGRESS:{"format_id":%(info.format_id)j,"downloaded_bytes":%(progress.downloaded_bytes|0)s,"total_bytes":%(progress.total_bytes|0)s,"total_bytes_estimate":%(progress.total_bytes_estimate|0)s,"speed":%(progress.speed|0)s,"eta":%(progress.eta|0)s}`, "--progress-template", `postprocess:FG_POST:{"postprocessor":%(progress.postprocessor)j,"status":%(progress.status)j}`)
	if options.LimitBytesPerSecond > 0 {
		args = append(args, "--limit-rate", strconv.FormatInt(options.LimitBytesPerSecond, 10))
	}
	if selection.VideoFormat != "" {
		args = append(args, "--merge-output-format", selection.Container, "--remux-video", selection.Container)
	} else {
		args = append(args, "--extract-audio", "--audio-format", selection.Container)
		if selection.Container == "mp3" {
			args = append(args, "--audio-quality", strconv.Itoa(selection.MP3Kbps)+"K")
		}
	}
	args = append(args, sourceArgs...)
	parser := newProgressParser(options.Events)
	// yt-dlp routes postprocessor screen updates to stderr when --print uses
	// stdout. Each stream needs its own line buffer to prevent interleaving.
	stderr := newProgressParser(options.Events)
	err = runStreams(ctx, c.tools.YTDLP, args, workspace, parser, stderr, 32<<20)
	if err != nil {
		return result, err
	}
	if err := parser.finish(); err != nil {
		return result, err
	}
	expected := filepath.Join(workspace, "asset."+selection.Container)
	if filepath.Clean(parser.output) != expected {
		return result, errors.New("media tool returned an unexpected output path")
	}
	info, err := os.Lstat(expected)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return result, errors.New("media output is missing or not a regular file")
	}
	inspection, err := c.inspect(ctx, expected)
	if err != nil {
		return result, err
	}
	if err := inspection.validate(video.Duration, selection); err != nil {
		return result, err
	}
	if err := inspection.matchesSelection(video, selection); err != nil {
		return result, err
	}
	result.Output, result.Inspection = expected, inspection
	return result, nil
}

type Stream struct {
	Kind      string `json:"codec_type"`
	Codec     string `json:"codec_name"`
	Height    int    `json:"height,omitempty"`
	FrameRate string `json:"r_frame_rate,omitempty"`
}

type Inspection struct {
	Streams []Stream `json:"streams"`
	Format  struct {
		Name     string `json:"format_name"`
		Duration string `json:"duration"`
	} `json:"format"`
}

func (c *Client) inspect(ctx context.Context, file string) (Inspection, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	data, err := capture(ctx, c.tools.FFprobe, []string{"-v", "error", "-show_entries", "stream=codec_type,codec_name,height,r_frame_rate:format=format_name,duration", "-of", "json", file}, filepath.Dir(file), 1<<20)
	if err != nil {
		return Inspection{}, err
	}
	var value Inspection
	if json.Unmarshal(data, &value) != nil || len(value.Streams) > 16 {
		return Inspection{}, ErrMetadata
	}
	return value, nil
}

func (i Inspection) validate(duration float64, selection Selection) error {
	d, err := strconv.ParseFloat(i.Format.Duration, 64)
	if err != nil || math.IsNaN(d) || math.IsInf(d, 0) || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || d <= 0 || math.Abs(d-duration) > math.Max(2, duration*0.05) {
		return errors.New("media duration does not match selected source")
	}
	video, audio := 0, 0
	for _, stream := range i.Streams {
		switch stream.Kind {
		case "video":
			video++
		case "audio":
			audio++
		}
	}
	if audio != 1 || (selection.VideoFormat != "" && video != 1) || (selection.VideoFormat == "" && video != 0) {
		return errors.New("media output has unexpected streams")
	}
	formats := map[string]string{"mp4": "mov,mp4,m4a,3gp,3g2,mj2", "m4a": "mov,mp4,m4a,3gp,3g2,mj2", "webm": "matroska,webm", "mkv": "matroska,webm", "opus": "ogg", "mp3": "mp3"}
	if i.Format.Name != formats[selection.Container] {
		return errors.New("media output container does not match selection")
	}
	return nil
}

func codecFamily(codec string) string {
	codec = strings.ToLower(strings.SplitN(codec, ".", 2)[0])
	switch codec {
	case "avc1", "avc3":
		return "h264"
	case "mp4a":
		return "aac"
	case "av01":
		return "av1"
	case "vp09":
		return "vp9"
	case "hvc1", "hev1":
		return "hevc"
	}
	return codec
}

func (i Inspection) matchesSelection(video Video, selection Selection) error {
	var selectedVideo, selectedAudio Format
	for _, format := range video.Formats {
		if format.ID == selection.VideoFormat {
			selectedVideo = format
			if selection.AudioFormat == "" {
				selectedAudio = format
			}
		}
		if format.ID == selection.AudioFormat {
			selectedAudio = format
		}
	}
	for _, stream := range i.Streams {
		if stream.Kind == "video" {
			if stream.Codec != codecFamily(selectedVideo.VideoCodec) || selectedVideo.Height > 0 && stream.Height != selectedVideo.Height {
				return errors.New("media video codec or resolution does not match selection")
			}
			if selectedVideo.FPS > 0 {
				numerator, denominator, ok := strings.Cut(stream.FrameRate, "/")
				n, e1 := strconv.ParseFloat(numerator, 64)
				d, e2 := strconv.ParseFloat(denominator, 64)
				if !ok || e1 != nil || e2 != nil || !(d > 0) || !(n > 0) || math.IsInf(n, 0) || math.IsInf(d, 0) || math.Abs(n/d-selectedVideo.FPS) > math.Max(0.1, selectedVideo.FPS*0.01) {
					return errors.New("media frame rate does not match selection")
				}
			}
		}
		if stream.Kind == "audio" {
			expected := codecFamily(selectedAudio.AudioCodec)
			if selection.Container == "mp3" {
				expected = "mp3"
			}
			if stream.Codec != expected {
				return errors.New("media audio codec does not match selection")
			}
		}
	}
	return nil
}
