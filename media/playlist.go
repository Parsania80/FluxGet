package media

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const PlaylistPageSize = 100
const PlaylistBatchLimit = 1000

var playlistID = regexp.MustCompile(`^(PL|UU)[A-Za-z0-9_-]{1,126}$`)
var mixID = regexp.MustCompile(`^RD[A-Za-z0-9_-]{1,126}$`)
var ErrMix = errors.New("YouTube Mix import is not supported")

// NormalizePlaylistURL accepts finite playlists, never channels or radio mixes.
func NormalizePlaylistURL(raw string) (string, error) {
	if len(raw) > 8192 {
		return "", ErrURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return "", ErrURL
	}
	switch strings.ToLower(u.Hostname()) {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		if u.Path != "/playlist" && u.Path != "/watch" && !strings.HasPrefix(u.Path, "/shorts/") {
			return "", ErrURL
		}
	case "youtu.be":
		if !videoID.MatchString(strings.TrimPrefix(u.Path, "/")) {
			return "", ErrURL
		}
	default:
		return "", ErrURL
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["list"]) != 1 {
		return "", ErrURL
	}
	if mixID.MatchString(q.Get("list")) {
		return "", ErrMix
	}
	if !playlistID.MatchString(q.Get("list")) {
		return "", ErrURL
	}
	if u.Path != "/playlist" {
		if _, err := NormalizeVideoURL(raw); err != nil {
			return "", err
		}
	}
	return "https://www.youtube.com/playlist?list=" + q.Get("list"), nil
}

type PlaylistEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Index       int    `json:"playlist_index"`
	Unavailable bool   `json:"unavailable"`
}

type PlaylistPage struct {
	ID, Title string
	Entries   []PlaylistEntry
	First     int
	HasMore   bool
}

// PlaylistPosition is saved with each job; the batch receipt survives removal.
type PlaylistPosition struct {
	Batch string `json:"batch"`
	ID    string `json:"id"`
	Title string `json:"title"`
	Index int    `json:"index"`
}

func ValidPlaylistBatch(id string) bool {
	decoded, err := hex.DecodeString(id)
	return err == nil && len(decoded) == 16 && len(id) == 32
}

func (p PlaylistPosition) Validate() error {
	if !ValidPlaylistBatch(p.Batch) || !playlistID.MatchString(p.ID) || len(p.Title) > 4096 || strings.IndexFunc(p.Title, unicode.IsControl) >= 0 || p.Index < 1 || p.Index > 1000000 {
		return ErrMetadata
	}
	return nil
}

type PlaylistResult struct {
	Index int    `json:"index"`
	JobID int    `json:"job_id,omitempty"`
	Error string `json:"error,omitempty"`
}

type PlaylistSummary struct {
	Title                                        string `json:"title"`
	Completed, Failed, Pending, Skipped, Removed int
}

func (c *Client) Playlist(ctx context.Context, rawURL string, first int) (PlaylistPage, error) {
	u, err := NormalizePlaylistURL(rawURL)
	if err != nil {
		return PlaylistPage{}, err
	}
	if first < 1 || first > 1000000-PlaylistPageSize {
		return PlaylistPage{}, ErrURL
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	release, err := c.acquire(ctx)
	if err != nil {
		return PlaylistPage{}, err
	}
	defer release()
	args := append(c.baseArgs(), "--use-extractors", "youtube.*", "--yes-playlist", "--flat-playlist", "--lazy-playlist", "--playlist-items", strconv.Itoa(first)+":"+strconv.Itoa(first+PlaylistPageSize), "--dump-single-json", "--skip-download", "--", u)
	data, err := capture(ctx, c.tools.YTDLP, args, "", 2<<20)
	if err != nil {
		return PlaylistPage{}, err
	}
	return parsePlaylist(data, strings.TrimPrefix(u, "https://www.youtube.com/playlist?list="), first)
}

func parsePlaylist(data []byte, id string, first int) (PlaylistPage, error) {
	var raw struct {
		ID, Title, Availability string
		Type                    string `json:"_type"`
		Entries                 []json.RawMessage
	}
	if json.Unmarshal(data, &raw) != nil || raw.Type != "playlist" || raw.ID != id || !playlistID.MatchString(id) || len(raw.Title) > 4096 || len(raw.Entries) > PlaylistPageSize+1 || raw.Availability != "" && raw.Availability != "public" && raw.Availability != "unlisted" {
		return PlaylistPage{}, ErrMetadata
	}
	p := PlaylistPage{ID: id, Title: playlistTitle(raw.Title), First: first, HasMore: len(raw.Entries) > PlaylistPageSize}
	for i, data := range raw.Entries[:min(len(raw.Entries), PlaylistPageSize)] {
		var entry struct {
			PlaylistEntry
			Availability string
			Live         bool   `json:"is_live"`
			LiveStatus   string `json:"live_status"`
		}
		if json.Unmarshal(data, &entry) != nil || len(entry.Title) > 4096 || len(entry.ID) > 128 {
			return PlaylistPage{}, ErrMetadata
		}
		if entry.Index != 0 && entry.Index != first+i {
			return PlaylistPage{}, ErrMetadata
		}
		entry.Index = first + i
		entry.Unavailable = entry.Unavailable || !videoID.MatchString(entry.ID) || entry.Live || entry.LiveStatus == "is_live" || entry.LiveStatus == "is_upcoming" || entry.LiveStatus == "post_live" || entry.Title == "[Private video]" || entry.Title == "[Deleted video]" || entry.Availability != "" && entry.Availability != "public" && entry.Availability != "unlisted"
		if entry.Title == "" {
			entry.Title = "Unavailable entry"
		}
		entry.Title = playlistTitle(entry.Title)
		p.Entries = append(p.Entries, entry.PlaylistEntry)
	}
	return p, nil
}

func playlistTitle(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)
}
