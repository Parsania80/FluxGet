package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Parsania80/FluxGet/internal/platformfile"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

type cleanupIntent struct {
	Action         string          `json:"action"`
	Identity       partialIdentity `json:"identity"`
	CheckpointHash string          `json:"checkpoint_sha256"`
}

type queueJob struct {
	IntakeID       string         `json:"browser_intake_id,omitempty"`
	ID             int            `json:"id"`
	URL            string         `json:"url"`
	Destination    string         `json:"destination"`
	State          string         `json:"state"`
	ExpectedSHA256 string         `json:"expected_sha256,omitempty"`
	Cleanup        *cleanupIntent `json:"cleanup,omitempty"`
	AutomaticName  bool           `json:"automatic_name,omitempty"`
	Message        string         `json:"message,omitempty"`
	Progress       string         `json:"progress,omitempty"`
	Conflict       string         `json:"conflict,omitempty"`
	OutputPath     string         `json:"output_path,omitempty"`
	SavedProgress  *float64       `json:"saved_progress,omitempty"`
}
type queueData struct {
	BrowserReceipts map[string]browserReceipt `json:"browser_receipts,omitempty"`
	Version         int                       `json:"version"`
	NextID          int                       `json:"next_id"`
	Jobs            []queueJob                `json:"jobs"`
}

func queuePath() (string, error) {
	dir, err := jobDirectory()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), "queue.json"), nil
}

func validQueueURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func loadQueue(path string) (queueData, error) {
	q := queueData{Version: 2, NextID: 1}
	f, err := platformfile.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return q, nil
	}
	if err != nil {
		return q, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return q, err
	}
	if len(data) > 1<<20 {
		return q, errors.New("queue exceeds size limit")
	}
	q = queueData{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&q); err != nil {
		return q, err
	}
	if dec.Decode(new(any)) != io.EOF || (q.Version != 1 && q.Version != 2 && q.Version != 3) || q.NextID < 1 {
		return q, errors.New("invalid queue metadata")
	}
	ids := make(map[int]bool)
	if len(q.BrowserReceipts) > 2048 || len(q.BrowserReceipts) > 0 && q.Version != 3 {
		return q, errors.New("invalid browser handoff history")
	}
	for key, r := range q.BrowserReceipts {
		if !validIntakeID(key) || r.JobID < 1 || r.JobID >= q.NextID || r.Expires <= 0 || (r.Stage != "pending" && r.Stage != "active" && r.Stage != "aborted") || (r.Kind != "capture" && r.Kind != "link") {
			return q, errors.New("invalid browser receipt")
		}
	}
	destinations := make(map[string]bool)
	for _, j := range q.Jobs {
		if j.IntakeID != "" {
			r, ok := q.BrowserReceipts[j.IntakeID]
			if !ok || r.JobID != j.ID || q.Version != 3 || r.Stage == "pending" && j.State != "paused" {
				return q, errors.New("invalid browser job")
			}
		}
		if j.SavedProgress != nil && (*j.SavedProgress < 0 || *j.SavedProgress > 1) {
			return q, errors.New("invalid saved progress")
		}
		if len(j.Message) > 4096 || len(j.Progress) > 1024 || (j.Conflict != "" && (!filepath.IsAbs(j.Conflict) || filepath.Clean(j.Conflict) != j.Conflict)) {
			return q, errors.New("invalid queue details")
		}
		if j.OutputPath != "" && (j.State != "completed" || !filepath.IsAbs(j.OutputPath) || filepath.Clean(j.OutputPath) != j.OutputPath) {
			return q, errors.New("invalid completed output path")
		}
		if j.ID < 1 || j.ID >= q.NextID || ids[j.ID] || destinations[j.Destination] || !filepath.IsAbs(j.Destination) || filepath.Clean(j.Destination) != j.Destination || !validQueueURL(j.URL) {
			return q, errors.New("invalid queue job")
		}
		switch j.State {
		case "queued", "running", "paused", "failed", "completed", "canceled", "resetting":
		default:
			return q, errors.New("invalid queue state")
		}
		if j.ExpectedSHA256 != "" {
			digest, err := hex.DecodeString(j.ExpectedSHA256)
			if err != nil || len(digest) != sha256.Size {
				return q, errors.New("invalid queue checksum")
			}
		}
		if j.State == "resetting" {
			if j.Cleanup == nil || (j.Cleanup.Action != "restart" && j.Cleanup.Action != "discard") || j.Cleanup.Identity.Inode == 0 {
				return q, errors.New("invalid cleanup intent")
			}
			digest, err := hex.DecodeString(j.Cleanup.CheckpointHash)
			if err != nil || len(digest) != sha256.Size {
				return q, errors.New("invalid cleanup checkpoint hash")
			}
		} else if j.Cleanup != nil {
			return q, errors.New("unexpected cleanup intent")
		}
		ids[j.ID] = true
		destinations[j.Destination] = true
	}
	return q, nil
}

func saveQueue(path string, q queueData) error {
	if q.Version < 3 {
		q.Version = 2
	}
	if len(q.BrowserReceipts) > 0 {
		q.Version = 3
	}
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("queue exceeds size limit")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".fluxget-queue-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("publish queue: %w", err)
	}
	return nil
}
