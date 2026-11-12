// Package media is the isolated stage-1 media backend. It does not change the
// persistent queue or desktop UI and does not share the HTTP engine's limiter.
package media

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"strings"
)

var (
	ErrURL       = errors.New("expected a supported public YouTube video URL")
	ErrPlaylist  = errors.New("playlist selection is not implemented in the stage-1 adapter")
	ErrMetadata  = errors.New("invalid or unsupported media metadata")
	ErrSelection = errors.New("requested media format is not available")
	videoID      = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	formatID     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// NormalizeVideoURL deliberately strips tracking and list parameters. A watch
// URL with a list is one video here; full playlist selection belongs to stage 4.
func NormalizeVideoURL(raw string) (string, error) {
	if len(raw) > 8192 {
		return "", ErrURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return "", ErrURL
	}
	var id string
	switch strings.ToLower(u.Hostname()) {
	case "youtu.be":
		id = strings.TrimPrefix(u.Path, "/")
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		if u.Path == "/playlist" {
			return "", ErrPlaylist
		}
		switch {
		case u.Path == "/watch":
			if len(u.Query()["v"]) != 1 {
				return "", ErrURL
			}
			id = u.Query().Get("v")
		case strings.HasPrefix(u.Path, "/shorts/"):
			id = strings.TrimPrefix(u.Path, "/shorts/")
		default:
			return "", ErrURL
		}
	default:
		return "", ErrURL
	}
	if !videoID.MatchString(id) {
		return "", ErrURL
	}
	return "https://www.youtube.com/watch?v=" + id, nil
}

// Format contains only display/selection metadata, never signed transfer URLs.
type Format struct {
	ID            string  `json:"format_id"`
	Extension     string  `json:"ext"`
	VideoCodec    string  `json:"vcodec"`
	AudioCodec    string  `json:"acodec"`
	Height        int     `json:"height"`
	FPS           float64 `json:"fps"`
	Size          int64   `json:"filesize"`
	EstimatedSize int64   `json:"filesize_approx"`
	AudioBitrate  float64 `json:"abr"`
	Language      string  `json:"language"`
	DRM           bool    `json:"has_drm"`
	Protocol      string  `json:"protocol"`
}

type Video struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Duration float64  `json:"duration"`
	Formats  []Format `json:"formats"`
}

func parseVideo(data []byte) (Video, error) {
	var raw struct {
		Video
		Type         string `json:"_type"`
		Live         bool   `json:"is_live"`
		LiveStatus   string `json:"live_status"`
		Availability string `json:"availability"`
	}
	if json.Unmarshal(data, &raw) != nil || !videoID.MatchString(raw.ID) || len(raw.Title) > 4096 || raw.Type == "playlist" || raw.Live || raw.LiveStatus == "is_upcoming" || raw.LiveStatus == "post_live" || raw.Duration <= 0 || math.IsInf(raw.Duration, 0) || len(raw.Formats) > 2048 {
		return Video{}, ErrMetadata
	}
	if raw.Availability != "" && raw.Availability != "public" && raw.Availability != "unlisted" {
		return Video{}, ErrMetadata
	}
	formats := make([]Format, 0, len(raw.Formats))
	seen := make(map[string]bool)
	for _, f := range raw.Formats {
		if f.DRM || !formatID.MatchString(f.ID) || seen[f.ID] || len(f.Language) > 64 || len(f.VideoCodec) > 128 || len(f.AudioCodec) > 128 || f.Height < 0 || f.Size < 0 || f.EstimatedSize < 0 {
			continue
		}
		seen[f.ID] = true
		formats = append(formats, f)
	}
	raw.Formats = formats
	if len(formats) == 0 {
		return Video{}, ErrMetadata
	}
	return raw.Video, nil
}

type Selection struct {
	VideoFormat string // Optional for audio-only; IDs must occur in Video.Formats.
	AudioFormat string
	Container   string // mp4, webm, mkv, m4a, opus, or mp3.
	MP3Kbps     int
}

func (s Selection) validate(v Video) (string, error) {
	find := func(id string) (Format, bool) {
		for _, f := range v.Formats {
			if id != "" && f.ID == id && formatID.MatchString(id) && !f.DRM && supportedProtocol(f.Protocol) {
				return f, true
			}
		}
		return Format{}, false
	}
	vf, hasVideo := find(s.VideoFormat)
	af, hasAudio := find(s.AudioFormat)
	if hasVideo && vf.VideoCodec != "none" && vf.VideoCodec != "" {
		if s.Container != "mp4" && s.Container != "webm" && s.Container != "mkv" {
			return "", ErrSelection
		}
		if s.Container == "mp4" && !strings.HasPrefix(vf.VideoCodec, "avc1") && !strings.HasPrefix(vf.VideoCodec, "h264") {
			return "", ErrSelection
		}
		if s.Container == "webm" && !strings.HasPrefix(vf.VideoCodec, "vp9") && !strings.HasPrefix(vf.VideoCodec, "vp09") && !strings.HasPrefix(vf.VideoCodec, "av01") {
			return "", ErrSelection
		}
		if s.AudioFormat == "" && vf.AudioCodec != "none" && vf.AudioCodec != "" {
			af, hasAudio = vf, true
		} else if !hasAudio || af.VideoCodec != "none" || vf.AudioCodec != "none" {
			return "", ErrSelection
		}
		if !hasAudio || af.AudioCodec == "none" || af.AudioCodec == "" || !audioCompatible(s.Container, af.AudioCodec) {
			return "", ErrSelection
		}
		if s.AudioFormat != "" {
			return vf.ID + "+" + af.ID, nil
		}
		return vf.ID, nil
	}
	if s.VideoFormat != "" || !hasAudio || af.VideoCodec != "none" || af.AudioCodec == "none" || af.AudioCodec == "" {
		return "", ErrSelection
	}
	switch s.Container {
	case "m4a":
		if !audioCompatible("mp4", af.AudioCodec) {
			return "", ErrSelection
		}
	case "opus":
		if af.AudioCodec != "opus" {
			return "", ErrSelection
		}
	case "mp3":
		if s.MP3Kbps != 128 && s.MP3Kbps != 192 && s.MP3Kbps != 256 && s.MP3Kbps != 320 {
			return "", ErrSelection
		}
	default:
		return "", ErrSelection
	}
	return af.ID, nil
}

func supportedProtocol(protocol string) bool {
	switch protocol {
	case "https", "http", "http_dash_segments", "m3u8_native":
		return true
	default:
		return false
	}
}

func audioCompatible(container, codec string) bool {
	return container == "mkv" || container == "mp4" && (strings.HasPrefix(codec, "mp4a") || codec == "aac") || container == "webm" && (codec == "opus" || codec == "vorbis")
}
