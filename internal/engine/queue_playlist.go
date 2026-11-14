package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"sort"

	"github.com/Parsania80/FluxGet/browserbridge"
	"github.com/Parsania80/FluxGet/media"
)

type playlistReceipt struct {
	Fingerprint string                 `json:"fingerprint"`
	Title       string                 `json:"title"`
	Results     []media.PlaylistResult `json:"results"`
}

// Build the entire admission before publishing it. No transfer can start until
// the owner has persisted both jobs and their replay receipt.
func addPlaylist(q *queueData, batch string, jobs []queueJob) ([]media.PlaylistResult, error) {
	if !media.ValidPlaylistBatch(batch) || len(jobs) == 0 || len(jobs) > media.PlaylistBatchLimit {
		return nil, errors.New("select between 1 and 1,000 playlist entries")
	}
	data, err := json.Marshal(jobs)
	if err != nil || len(data) > 900<<10 {
		return nil, errors.New("playlist selection is too large; submit a smaller batch")
	}
	hash := sha256.Sum256(data)
	fingerprint := hex.EncodeToString(hash[:])
	if old, ok := q.Playlists[batch]; ok {
		if old.Fingerprint != fingerprint {
			return nil, errors.New("playlist submission changed; prepare a new batch")
		}
		return old.Results, nil
	}
	if len(q.Playlists) >= 128 {
		return nil, errors.New("playlist batch history is full; keep the saved queue for inspection")
	}
	previous := 0
	var playlistID, title string
	for _, j := range jobs {
		if j.Media == nil || j.Media.Playlist == nil || j.Media.Playlist.Validate() != nil || j.Media.Playlist.Batch != batch || j.Media.Playlist.Index <= previous || j.State != "" && j.State != "paused" {
			return nil, errors.New("invalid playlist order or selection")
		}
		p := j.Media.Playlist
		if previous > 0 && (p.ID != playlistID || p.Title != title) {
			return nil, errors.New("playlist entries must belong to one playlist")
		}
		playlistID, title, previous = p.ID, p.Title, p.Index
	}
	candidate := *q
	candidate.Jobs = append([]queueJob(nil), q.Jobs...)
	candidate.Playlists = maps.Clone(q.Playlists)
	if candidate.Playlists == nil {
		candidate.Playlists = make(map[string]playlistReceipt)
	}
	results := make([]media.PlaylistResult, 0, len(jobs))
	for _, j := range jobs {
		result := media.PlaylistResult{Index: j.Media.Playlist.Index}
		id, e := desktopAdd(&candidate, j, false)
		if e != nil {
			result.Error = browserbridge.Diagnostic(e.Error())
		} else {
			result.JobID = id
		}
		results = append(results, result)
	}
	candidate.Version = 6
	candidate.Playlists[batch] = playlistReceipt{Fingerprint: fingerprint, Title: title, Results: results}
	data, err = json.Marshal(candidate)
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("saved queue is full; submit a smaller playlist batch")
	}
	*q = candidate
	return results, nil
}

func playlistSummaries(q queueData) []media.PlaylistSummary {
	states := make(map[int]string, len(q.Jobs))
	for _, j := range q.Jobs {
		states[j.ID] = j.State
	}
	keys := make([]string, 0, len(q.Playlists))
	for key := range q.Playlists {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var summaries []media.PlaylistSummary
	for _, key := range keys {
		r := q.Playlists[key]
		s := media.PlaylistSummary{Title: r.Title}
		for _, item := range r.Results {
			switch {
			case item.JobID == 0:
				s.Skipped++
			case states[item.JobID] == "completed":
				s.Completed++
			case states[item.JobID] == "failed" || states[item.JobID] == "canceled":
				s.Failed++
			case states[item.JobID] == "":
				s.Removed++
			default:
				s.Pending++
			}
		}
		summaries = append(summaries, s)
	}
	return summaries
}

func validatePlaylistReceipts(q queueData) error {
	if len(q.Playlists) > 128 || len(q.Playlists) > 0 && q.Version < 6 {
		return errors.New("invalid playlist batch history")
	}
	for key, r := range q.Playlists {
		digest, e := hex.DecodeString(r.Fingerprint)
		if !media.ValidPlaylistBatch(key) || e != nil || len(digest) != 32 || len(r.Title) > 4096 || len(r.Results) == 0 || len(r.Results) > media.PlaylistBatchLimit {
			return errors.New("invalid playlist receipt")
		}
		previous := 0
		for _, item := range r.Results {
			if item.Index <= previous || item.Index > 1000000 || item.JobID < 0 || item.JobID >= q.NextID || len(item.Error) > 4096 || (item.JobID == 0) == (item.Error == "") {
				return errors.New("invalid playlist result")
			}
			previous = item.Index
		}
	}
	for _, j := range q.Jobs {
		if j.Media == nil || j.Media.Playlist == nil {
			continue
		}
		p := j.Media.Playlist
		r, ok := q.Playlists[p.Batch]
		found := false
		for _, item := range r.Results {
			found = found || item.Index == p.Index && item.JobID == j.ID
		}
		if q.Version < 6 || !ok || r.Title != p.Title || !found {
			return errors.New("playlist job has no matching receipt")
		}
	}
	return nil
}
