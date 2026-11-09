package main

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// File ranges are half-open; End == -1 means an unknown-length stream.
type transferSegment struct {
	Start      int64  `json:"start"`
	End        int64  `json:"end"`
	Downloaded int64  `json:"downloaded"`
	State      string `json:"state"`
}
type segmentSnapshot struct {
	Mode   string            `json:"transfer_mode"`
	Limit  int               `json:"connection_limit"`
	Parts  []transferSegment `json:"segments"`
	Target int               `json:"worker_target,omitempty"`
	Reason string            `json:"worker_reason,omitempty"`
}
type segmentObserver interface{ ReportSegments(segmentSnapshot) }

func (s segmentSnapshot) progress() string {
	var received, total int64
	for _, part := range s.Parts {
		if part.End < part.Start {
			return ""
		}
		total += part.End - part.Start
		received += part.Downloaded
	}
	if total <= 0 {
		return ""
	}
	return fmt.Sprintf("Downloaded %d/%d bytes (%.1f%%)", received, total, float64(received)*100/float64(total))
}

const (
	segmentWaiting int32 = iota
	segmentConnecting
	segmentDownloading
	segmentVerifying
	segmentComplete
	segmentPaused
	segmentFailed
)

var segmentStates = [...]string{"waiting", "connecting", "downloading", "verifying", "completed", "paused", "failed"}

type segmentPart struct {
	start, end int64
	received   atomic.Int64
	state      atomic.Int32
	retiring   atomic.Bool
}

func (p *segmentPart) setState(state int32) {
	if p != nil {
		p.state.Store(state)
	}
}
func (p *segmentPart) setReceived(n int64) {
	if p != nil {
		p.received.Store(n)
	}
}
func (p *segmentPart) add(n int64) {
	if p != nil {
		p.received.Add(n)
	}
}

type segmentSession struct {
	observer segmentObserver
	mode     string
	workers  atomic.Pointer[workerSettings]
	parts    []*segmentPart
	stop     chan struct{}
	done     chan struct{}
}

func newSegmentSession(output io.Writer, mode string, limit int, ranges []transferSegment) *segmentSession {
	observer, ok := output.(segmentObserver)
	if !ok {
		return nil
	}
	s := &segmentSession{observer: observer, mode: mode, stop: make(chan struct{}), done: make(chan struct{})}
	s.workers.Store(&workerSettings{Maximum: limit, Target: limit})
	for _, r := range ranges {
		p := &segmentPart{start: r.Start, end: r.End}
		p.received.Store(r.Downloaded)
		for i, name := range segmentStates {
			if r.State == name {
				p.state.Store(int32(i))
				break
			}
		}
		s.parts = append(s.parts, p)
	}
	s.publish()
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.publish()
			case <-s.stop:
				return
			}
		}
	}()
	return s
}
func (s *segmentSession) part(i int) *segmentPart {
	if s == nil {
		return nil
	}
	return s.parts[i]
}
func (s *segmentSession) publish() {
	if s == nil {
		return
	}
	workers := s.workers.Load()
	snapshot := segmentSnapshot{Mode: s.mode, Limit: workers.Maximum, Target: workers.Target, Reason: workers.Reason, Parts: make([]transferSegment, len(s.parts))}
	for i, p := range s.parts {
		n, state := p.received.Load(), p.state.Load()
		end := p.end
		if end < 0 && state == segmentComplete {
			end = p.start + n
		}
		if end >= p.start && n > end-p.start {
			n = end - p.start
		}
		if state == segmentComplete && end >= p.start {
			n = end - p.start
		}
		snapshot.Parts[i] = transferSegment{Start: p.start, End: end, Downloaded: n, State: segmentStates[state]}
		if p.retiring.Load() {
			snapshot.Parts[i].State = "retiring"
		}
	}
	s.observer.ReportSegments(snapshot)
}
func (s *segmentSession) finish(ctx context.Context, success bool) {
	if s == nil {
		return
	}
	close(s.stop)
	<-s.done
	for _, p := range s.parts {
		if p.state.Load() == segmentComplete {
			continue
		}
		switch {
		case success:
			p.setState(segmentComplete)
		case ctx.Err() != nil:
			p.setState(segmentPaused)
		default:
			p.setState(segmentFailed)
		}
	}
	s.publish()
}

func segmentsFromCheckpoint(c checkpoint, limit int) segmentSnapshot {
	s := segmentSnapshot{Mode: "sequential", Limit: 1}
	if c.parallel() {
		s.Mode, s.Limit = "parallel", limit
		for _, chunk := range c.Chunks {
			part := transferSegment{Start: chunk.Start, End: chunk.End, Downloaded: chunk.savedLength(), State: "paused"}
			if chunk.SHA256 != "" {
				part.Downloaded, part.State = chunk.End-chunk.Start, "saved"
			}
			s.Parts = append(s.Parts, part)
		}
	} else {
		end := int64(-1)
		if c.Total != nil {
			end = *c.Total
		}
		s.Parts = []transferSegment{{Start: 0, End: end, Downloaded: c.Completed, State: "paused"}}
	}
	return s
}

func liveSegmentLayouts(saved map[int]segmentSnapshot, writers map[int]*queueProgressWriter) map[int]segmentSnapshot {
	live := make(map[int]segmentSnapshot, len(saved)+len(writers))
	for id, layout := range saved {
		live[id] = layout
	}
	for id, writer := range writers {
		if layout := writer.segments.Load(); layout != nil {
			live[id] = *layout
		}
	}
	return live
}
