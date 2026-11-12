package media

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

type progressParser struct {
	pending []byte
	output  string
	events  chan<- Progress
}

func newProgressParser(events chan<- Progress) *progressParser {
	return &progressParser{events: events}
}

func (p *progressParser) Write(data []byte) (int, error) {
	n := len(data)
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			end = len(data)
		}
		if len(p.pending)+end > 1<<20 {
			return 0, ErrOutputLimit
		}
		p.pending = append(p.pending, data[:end]...)
		data = data[end:]
		if len(data) == 0 {
			break
		}
		data = data[1:]
		if err := p.line(strings.TrimSuffix(string(p.pending), "\r")); err != nil {
			return 0, err
		}
		p.pending = p.pending[:0]
	}
	return n, nil
}

func (p *progressParser) emit(event Progress) {
	select {
	case p.events <- event:
	default:
	}
}

func (p *progressParser) line(line string) error {
	switch {
	case strings.HasPrefix(line, "FG_PROGRESS:"):
		var raw struct {
			Status     string  `json:"status"`
			Downloaded int64   `json:"downloaded_bytes"`
			Total      int64   `json:"total_bytes"`
			Estimate   int64   `json:"total_bytes_estimate"`
			Speed      float64 `json:"speed"`
			ETA        float64 `json:"eta"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "FG_PROGRESS:")), &raw) != nil || raw.Downloaded < 0 || raw.Total < 0 || raw.Estimate < 0 || raw.Speed < 0 || math.IsNaN(raw.Speed) || math.IsInf(raw.Speed, 0) || raw.ETA < 0 {
			return errors.New("invalid structured media progress")
		}
		if raw.Total == 0 {
			raw.Total = raw.Estimate
		}
		p.emit(Progress{Phase: "download", Downloaded: raw.Downloaded, Total: raw.Total, Speed: raw.Speed, ETA: raw.ETA})
	case line == "FG_PHASE:download":
		p.emit(Progress{Phase: "download"})
	case line == "FG_PHASE:processing":
		p.emit(Progress{Phase: "processing"})
	case strings.HasPrefix(line, "FG_POST:"):
		var post struct{ Postprocessor, Status string }
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "FG_POST:")), &post) != nil {
			return errors.New("invalid structured media processing event")
		}
		if post.Status == "started" {
			switch post.Postprocessor {
			case "Merger", "VideoRemuxer", "ExtractAudio", "VideoConvertor", "FixupM4a", "FixupM3u8":
				p.emit(Progress{Phase: "processing"})
			}
		}
	case strings.HasPrefix(line, "FG_RESULT:"):
		if p.output != "" || json.Unmarshal([]byte(strings.TrimPrefix(line, "FG_RESULT:")), &p.output) != nil || len(p.output) > 8192 {
			return errors.New("invalid structured media output")
		}
	}
	return nil
}

func (p *progressParser) finish() error {
	if len(p.pending) > 0 {
		return errors.New("truncated structured media record")
	}
	if p.output == "" {
		return errors.New("media tool did not confirm its output")
	}
	return nil
}
