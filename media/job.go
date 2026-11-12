package media

import (
	"math"
	"path/filepath"
)

// Job is the bounded, URL-free selection saved by the queue. Transfer URLs are
// freshly resolved by yt-dlp; only the selected source formats are persisted.
type Job struct {
	Version   int       `json:"version"`
	Manifest  string    `json:"manifest"`
	Video     Video     `json:"video"`
	Selection Selection `json:"selection"`
}

func NewJob(video Video, selection Selection, manifest string) (Job, error) {
	job := Job{Version: 1, Manifest: manifest, Video: video, Selection: selection}
	job.Video.Formats = nil
	for _, format := range video.Formats {
		if format.ID == selection.VideoFormat || format.ID == selection.AudioFormat {
			job.Video.Formats = append(job.Video.Formats, format)
		}
	}
	return job, job.Validate()
}

func (j Job) Validate() error {
	if j.Version != 1 || !filepath.IsAbs(j.Manifest) || len(j.Manifest) > 4096 || !videoID.MatchString(j.Video.ID) || len(j.Video.Title) > 4096 || j.Video.Duration <= 0 || math.IsNaN(j.Video.Duration) || math.IsInf(j.Video.Duration, 0) || len(j.Video.Formats) < 1 || len(j.Video.Formats) > 2 {
		return ErrMetadata
	}
	seen := map[string]bool{}
	for _, f := range j.Video.Formats {
		if seen[f.ID] || (f.ID != j.Selection.VideoFormat && f.ID != j.Selection.AudioFormat) || len(f.Language) > 64 || len(f.VideoCodec) > 128 || len(f.AudioCodec) > 128 || f.Height < 0 || f.Height > 32768 || f.Size < 0 || f.EstimatedSize < 0 || f.FPS < 0 || f.FPS > 1000 || math.IsNaN(f.FPS) || math.IsInf(f.FPS, 0) || f.AudioBitrate < 0 || f.AudioBitrate > 100000 || math.IsNaN(f.AudioBitrate) || math.IsInf(f.AudioBitrate, 0) {
			return ErrMetadata
		}
		seen[f.ID] = true
	}
	return j.Selection.Validate(j.Video)
}

// Matches refuses to reuse media fragments after the selected representation
// changes. Availability of unrelated formats does not invalidate this job.
func (j Job) Matches(current Video) bool {
	if current.ID != j.Video.ID || math.Abs(current.Duration-j.Video.Duration) > math.Max(2, j.Video.Duration*0.01) || j.Selection.Validate(current) != nil {
		return false
	}
	for _, old := range j.Video.Formats {
		found := false
		for _, f := range current.Formats {
			if f.ID == old.ID {
				found = f.VideoCodec == old.VideoCodec && f.AudioCodec == old.AudioCodec && f.Height == old.Height && f.FPS == old.FPS && f.Language == old.Language && f.Protocol == old.Protocol && (old.Size == 0 || f.Size == 0 || old.Size == f.Size)
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
